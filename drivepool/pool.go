// Package drivepool pools multiple Google Drive accounts into one virtual
// storage volume: registered accounts are placement candidates, files are
// encrypted, split into fixed-size chunks, and each chunk is uploaded to
// whichever account currently has the most free space, and a shared
// Postgres manifest maps virtual file names back to the remote chunks that
// hold their bytes — scoped per user (see Pool.userID / manifest.scoped).
package drivepool

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"github.com/Souvik-223/rhino-framework/drivepool/auth"
	"github.com/Souvik-223/rhino-framework/drivepool/gdrive"
	"github.com/Souvik-223/rhino-framework/drivepool/manifest"
	"github.com/Souvik-223/rhino-framework/storage"
	"golang.org/x/oauth2"
)

// ErrAccountInUse is returned by RemoveAccount when the account still holds
// chunks for at least one non-deleted virtual file and force wasn't set —
// disconnecting it would make that file's data unreachable, since a chunk's
// only record of where its bytes live is chunks.account_id, and nothing
// else in the pool can rediscover a lost account/remote-file pairing.
var ErrAccountInUse = errors.New("drivepool: account still holds file data")

// DefaultChunkSize is used when Pool.ChunkSize is unset (zero).
const DefaultChunkSize = 128 << 20 // 128 MiB

// defaultMaxParallelUploads is used when Pool.MaxParallelUploads is unset.
const defaultMaxParallelUploads = 4

// Account is a registered Drive account: a placement candidate for Put and
// the source for Get. initErr is set when the account's client couldn't be
// built (e.g. a revoked/expired token, or an undecryptable stored token) —
// such accounts are skipped by placement and reported unhealthy by
// ListAccounts, never crash an operation outright.
type Account struct {
	ID      string
	Label   string
	Store   gdrive.RemoteStore
	initErr error
}

// Pool is scoped to exactly one user for its whole lifetime — userID is set
// once at construction (Open) and threaded into every manifest call
// internally, so callers (CLI commands, backend handlers) never pass it
// themselves. This mirrors how a Pool was already used in practice before
// the multi-tenant migration: one CLI process operates as one user, and the
// backend's poolCache already built one Pool per portal user.
type Pool struct {
	manifest  *manifest.Manifest
	userID    string
	clientCfg *oauth2.Config

	// newClient builds a RemoteStore from a token source; overridden in
	// tests to avoid any real network/OAuth dependency.
	newClient func(ctx context.Context, ts oauth2.TokenSource) (gdrive.RemoteStore, error)

	accounts map[string]*Account // keyed by manifest Account.ID

	// ChunkSize and MaxParallelUploads tune PutStream/GetStream; zero means
	// "use the default" (DefaultChunkSize / defaultMaxParallelUploads).
	ChunkSize          int64
	MaxParallelUploads int
}

func (p *Pool) chunkSize() int64 {
	if p.ChunkSize > 0 {
		return p.ChunkSize
	}
	return DefaultChunkSize
}

func (p *Pool) maxParallelUploads() int {
	if p.MaxParallelUploads > 0 {
		return p.MaxParallelUploads
	}
	return defaultMaxParallelUploads
}

// Open loads every account userID already has registered in m and builds a
// Drive client for each. An account whose token can't be decrypted or
// refreshed is kept in the pool with initErr set, rather than failing Open
// entirely.
func Open(ctx context.Context, m *manifest.Manifest, userID string, clientCfg *oauth2.Config) (*Pool, error) {
	p := &Pool{
		manifest:  m,
		userID:    userID,
		clientCfg: clientCfg,
		newClient: func(ctx context.Context, ts oauth2.TokenSource) (gdrive.RemoteStore, error) {
			return gdrive.NewClient(ctx, ts)
		},
		accounts: make(map[string]*Account),
	}

	rows, err := m.ListAccounts(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("drivepool: list accounts: %w", err)
	}

	for _, row := range rows {
		p.accounts[row.ID] = p.buildAccount(ctx, row)
	}

	return p, nil
}

func (p *Pool) buildAccount(ctx context.Context, row manifest.Account) *Account {
	a := &Account{ID: row.ID, Label: row.Label}

	if row.Token == nil {
		a.initErr = fmt.Errorf("load token: stored token could not be decrypted")
		return a
	}

	// context.Background(), not ctx: this TokenSource is stored on Account
	// and reused for the account's whole time in the (cross-request)
	// poolCache — golang.org/x/oauth2 captures whatever context it's given
	// here and reuses it for every future token refresh. ctx here is a
	// single HTTP request's context, canceled the moment that request
	// returns, so every later refresh (often well after the request that
	// built this Pool has finished) would fail with "context canceled"
	// otherwise, even though nothing is actually wrong with the request
	// that's using the account at that point.
	ts := p.clientCfg.TokenSource(context.Background(), row.Token)
	store, err := p.newClient(ctx, ts)
	if err != nil {
		a.initErr = fmt.Errorf("init client: %w", err)
		return a
	}

	a.Store = store
	return a
}

// AddAccount runs the CLI's interactive loopback OAuth consent flow
// (auth.RunConsentFlow: bind a local port, print a URL, block waiting for
// the browser to redirect back to that port) for a new account label,
// queries its starting quota, and registers it in the manifest (token
// included, encrypted at rest — see manifest.Manifest). This only works
// when the caller and the browser completing consent are the same machine
// — see AuthCodeURL/CompleteConsent for the web-redirect equivalent a
// remote HTTP backend needs instead.
func (p *Pool) AddAccount(ctx context.Context, label string) (*Account, error) {
	if err := p.checkLabelAvailable(ctx, label); err != nil {
		return nil, err
	}

	tok, err := auth.RunConsentFlow(ctx, p.clientCfg)
	if err != nil {
		return nil, fmt.Errorf("drivepool: consent flow: %w", err)
	}
	return p.finishAddAccount(ctx, label, tok)
}

// AuthCodeURL returns the Google consent URL for registering a new account
// under this Pool, using redirectURL as the OAuth callback. redirectURL
// must exactly match one of the "Authorized redirect URIs" configured for
// this OAuth client in Google Cloud Console — for a "Desktop app" client
// (the CLI's kind), Google exempts any http://localhost/127.0.0.1 URI from
// pre-registration, so local dev keeps working with the same
// client_secret.json the CLI uses; a real public domain requires a
// separate "Web application" type OAuth client with that exact redirect
// URI registered ahead of time. The same redirectURL must be passed to
// CompleteConsent for this same flow, since Google's token exchange
// requires it to match what was used to obtain the authorization code.
func (p *Pool) AuthCodeURL(redirectURL, state string) string {
	cfg := *p.clientCfg
	cfg.RedirectURL = redirectURL
	// prompt=select_account forces Google's account chooser even when the
	// browser already has an active session, so connecting a second account
	// doesn't silently re-authorize whichever one is currently signed in.
	// consent must be requested alongside it — Google only issues a
	// refresh_token on an account's first grant to this app; a bare
	// select_account re-auth for an already-authorized account can return
	// an access token with no refresh_token, which then works for about an
	// hour and fails permanently ("refresh token is not set") once it
	// expires, with no way to recover short of reconnecting from scratch.
	return cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "select_account consent"))
}

// CompleteConsent exchanges an authorization code obtained via
// AuthCodeURL's redirect for a token and finishes registering label —
// the web-backend equivalent of AddAccount, for callers that receive the
// code through their own HTTP callback route instead of driving
// auth.RunConsentFlow's loopback listener themselves.
func (p *Pool) CompleteConsent(ctx context.Context, redirectURL, code, label string) (*Account, error) {
	if err := p.checkLabelAvailable(ctx, label); err != nil {
		return nil, err
	}

	cfg := *p.clientCfg
	cfg.RedirectURL = redirectURL
	tok, err := cfg.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("drivepool: exchange code for token: %w", err)
	}
	return p.finishAddAccount(ctx, label, tok)
}

// checkLabelAvailable reports an error if label is already registered.
// Checked against the manifest (the durable source of truth for
// registered labels) rather than the in-memory p.accounts map, which is
// keyed by generated account ID, not label.
func (p *Pool) checkLabelAvailable(ctx context.Context, label string) error {
	_, err := p.manifest.GetAccount(ctx, p.userID, label)
	if err == nil {
		return fmt.Errorf("drivepool: account label %q already registered", label)
	}
	if !errors.Is(err, manifest.ErrNotFound) {
		return fmt.Errorf("drivepool: check existing accounts: %w", err)
	}
	return nil
}

// finishAddAccount does the token-independent second half of registering
// an account — build a client, query quota, and record everything in the
// manifest — shared by AddAccount (CLI, token from the loopback flow) and
// CompleteConsent (web backend, token from a code exchange).
func (p *Pool) finishAddAccount(ctx context.Context, label string, tok *oauth2.Token) (*Account, error) {
	if tok.RefreshToken == "" {
		return nil, fmt.Errorf("drivepool: Google didn't return a refresh token for %q — the account would work for about an hour and then fail permanently. Try again; if it keeps happening, revoke this app's access at myaccount.google.com/permissions and reconnect", label)
	}

	// context.Background(), not ctx — same reasoning as buildAccount: this
	// TokenSource is stored on the new Account and reused for every future
	// token refresh for as long as this Pool stays in poolCache, well
	// beyond the lifetime of the request that's connecting the account
	// right now.
	ts := p.clientCfg.TokenSource(context.Background(), tok)
	store, err := p.newClient(ctx, ts)
	if err != nil {
		return nil, fmt.Errorf("drivepool: init client: %w", err)
	}

	quota, err := store.Quota(ctx)
	if err != nil {
		return nil, fmt.Errorf("drivepool: query quota: %w", err)
	}

	id := storage.GenerateID()
	now := time.Now().UTC()
	if err := p.manifest.AddAccount(ctx, p.userID, manifest.Account{
		ID:      id,
		Label:   label,
		Token:   tok,
		AddedAt: now,
	}); err != nil {
		return nil, fmt.Errorf("drivepool: register account: %w", err)
	}
	if err := p.manifest.UpdateAccountQuota(ctx, p.userID, id, quota.Limit, quota.Usage, now); err != nil {
		return nil, fmt.Errorf("drivepool: record quota: %w", err)
	}

	a := &Account{ID: id, Label: label, Store: store}
	p.accounts[id] = a
	return a, nil
}

// RemoveAccount disconnects label. If it still holds chunks for any
// non-deleted file, removal is refused (ErrAccountInUse) unless force is
// set — those chunks' only pointer back to their remote Drive file would
// otherwise be lost the moment the account leaves the manifest. When force
// is set, the database itself nulls out those chunks' account_id (ON
// DELETE SET NULL) rather than leaving a dangling reference.
func (p *Pool) RemoveAccount(ctx context.Context, label string, force bool) error {
	row, err := p.manifest.GetAccount(ctx, p.userID, label)
	if err != nil {
		return err
	}
	if !force {
		n, err := p.manifest.CountActiveChunks(ctx, p.userID, row.ID)
		if err != nil {
			return fmt.Errorf("drivepool: check account usage: %w", err)
		}
		if n > 0 {
			return fmt.Errorf("%w: %q still holds %d chunk(s) from file(s) that would become unrecoverable", ErrAccountInUse, label, n)
		}
	}
	if err := p.manifest.RemoveAccount(ctx, p.userID, label); err != nil {
		return err
	}
	delete(p.accounts, row.ID)
	return nil
}

// AccountStatus is a live snapshot of one account's pooled quota, refreshed
// from Drive on every call rather than read from the manifest's cached copy.
type AccountStatus struct {
	Label     string
	Available int64
	Unlimited bool
	Limit     int64
	Usage     int64
	PhotoURL  string
	Err       error // set if this account's quota couldn't be refreshed
}

func (p *Pool) ListAccountStatus(ctx context.Context) ([]AccountStatus, error) {
	rows, err := p.manifest.ListAccounts(ctx, p.userID)
	if err != nil {
		return nil, err
	}

	out := make([]AccountStatus, 0, len(rows))
	for _, row := range rows {
		st := AccountStatus{Label: row.Label}
		a, ok := p.accounts[row.ID]
		if !ok || a.initErr != nil {
			st.Err = a.initErr
			if st.Err == nil {
				st.Err = fmt.Errorf("account not initialized")
			}
			out = append(out, st)
			continue
		}

		q, err := a.Store.Quota(ctx)
		if err != nil {
			st.Err = err
			out = append(out, st)
			continue
		}

		st.Limit, st.Usage, st.Unlimited = q.Limit, q.Usage, q.Unlimited
		st.Available = q.Available()
		st.PhotoURL = q.PhotoURL
		out = append(out, st)
	}
	return out, nil
}

// Put encrypts localPath and uploads it, split into one or more chunks
// spread across whichever registered accounts have the most free space,
// recording the result as a virtual file. It is a thin wrapper around
// PutStream so the CLI's on-disk-file use case needs no changes.
func (p *Pool) Put(ctx context.Context, localPath, virtualName string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("drivepool: open %q: %w", localPath, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("drivepool: stat %q: %w", localPath, err)
	}

	return p.PutStream(ctx, f, info.Size(), virtualName)
}

// uploadedChunk is the bookkeeping produced by uploadChunk for one
// successfully uploaded chunk, recorded into the manifest once every
// chunk in a PutStream call has finished.
type uploadedChunk struct {
	index           int
	accountID       string
	remoteFileID    string
	remoteFolderID  string
	plaintextSize   int64
	plaintextSHA256 string
	ciphertextMD5   string
	compressionAlgo string
	compressedSize  int64
}

// folderResolver ensures EnsureFolder is called at most once per account
// per PutStream call. Without this, two chunks concurrently placed on the
// same account could each see "no existing folder" and both create one,
// leaving duplicate same-named folders on that account.
type folderResolver struct {
	mu      sync.Mutex
	folders map[string]string // accountID -> folderID
}

func newFolderResolver() *folderResolver {
	return &folderResolver{folders: make(map[string]string)}
}

func (fr *folderResolver) resolve(ctx context.Context, acc *Account, name string) (string, error) {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	if id, ok := fr.folders[acc.ID]; ok {
		return id, nil
	}
	id, err := acc.Store.EnsureFolder(ctx, name)
	if err != nil {
		return "", err
	}
	fr.folders[acc.ID] = id
	return id, nil
}

// PutStream reads size bytes from r (the caller must ensure r yields
// exactly size bytes — the same contract gdrive.RemoteStore.Upload already
// has), splits them into chunkSize()-sized pieces, encrypts each chunk in
// memory (never staged to local disk), places it on whichever account has
// room via a placementTracker, and uploads chunks concurrently (bounded by
// maxParallelUploads()). This is what the web backend calls directly with
// an HTTP request body; Put wraps it for the CLI's local-file use case.
func (p *Pool) PutStream(ctx context.Context, r io.Reader, size int64, virtualName string) error {
	chunkSize := p.chunkSize()
	numChunks := 1
	if size > 0 {
		numChunks = int((size + chunkSize - 1) / chunkSize)
	}

	candidates := make([]*Account, 0, len(p.accounts))
	for _, a := range p.accounts {
		candidates = append(candidates, a)
	}

	var singleAccount *Account
	var tracker *placementTracker
	if numChunks == 1 {
		acc, err := pickAccount(ctx, candidates)
		if err != nil {
			return err
		}
		singleAccount = acc
	} else {
		t, err := newPlacementTracker(ctx, candidates)
		if err != nil {
			return err
		}
		tracker = t
	}

	fileKey := storage.NewEncryptionKey()
	fileID := storage.GenerateID()
	folders := newFolderResolver()

	fileHash := sha256.New()
	sem := make(chan struct{}, p.maxParallelUploads())
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	results := make([]*uploadedChunk, numChunks)

	remaining := size
	var readErr error
	for idx := 0; idx < numChunks; idx++ {
		n := chunkSize
		if remaining < n {
			n = remaining
		}
		buf := make([]byte, n)
		if n > 0 {
			if _, err := io.ReadFull(r, buf); err != nil {
				readErr = fmt.Errorf("drivepool: read chunk %d: %w", idx, err)
				break
			}
		}
		remaining -= n
		fileHash.Write(buf)

		acc := singleAccount
		if tracker != nil {
			accountID, err := tracker.reserve(n)
			if err != nil {
				readErr = err
				break
			}
			acc = p.accounts[accountID]
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, plaintext []byte, acc *Account) {
			defer wg.Done()
			defer func() { <-sem }()

			uc, err := p.uploadChunk(ctx, acc, folders, fileID, fileKey, idx, plaintext)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			results[idx] = uc
		}(idx, buf, acc)
	}
	wg.Wait()

	uploaded := make([]*uploadedChunk, 0, numChunks)
	for _, uc := range results {
		if uc != nil {
			uploaded = append(uploaded, uc)
		}
	}

	if readErr != nil {
		p.cleanupChunks(ctx, uploaded)
		return readErr
	}
	if firstErr != nil {
		p.cleanupChunks(ctx, uploaded)
		return firstErr
	}

	contentHash := hex.EncodeToString(fileHash.Sum(nil))
	vf := &manifest.VirtualFile{
		Name:        virtualName,
		Size:        size,
		ContentHash: contentHash,
		ChunkSize:   chunkSize,
		FileKey:     fileKey,
		Status:      manifest.StatusIncomplete,
	}
	if err := p.manifest.CreateVirtualFile(ctx, p.userID, vf); err != nil {
		p.cleanupChunks(ctx, uploaded)
		return fmt.Errorf("drivepool: record virtual file: %w", err)
	}

	for _, uc := range uploaded {
		accountID := uc.accountID
		if err := p.manifest.AddChunk(ctx, p.userID, &manifest.Chunk{
			VirtualFileID:   vf.ID,
			Index:           uc.index,
			AccountID:       &accountID,
			RemoteFileID:    uc.remoteFileID,
			RemoteFolderID:  uc.remoteFolderID,
			PlaintextSize:   uc.plaintextSize,
			PlaintextSHA256: uc.plaintextSHA256,
			CiphertextMD5:   uc.ciphertextMD5,
			CompressionAlgo: uc.compressionAlgo,
			CompressedSize:  uc.compressedSize,
		}); err != nil {
			return fmt.Errorf("drivepool: record chunk %d: %w", uc.index, err)
		}
	}

	return p.manifest.SetVirtualFileStatus(ctx, p.userID, vf.ID, manifest.StatusComplete)
}

// uploadChunk encrypts one chunk's plaintext entirely in memory (no local
// temp file) and uploads it to acc. The resulting *bytes.Reader satisfies
// the same io.ReaderAt gdrive.RemoteStore.Upload already requires for
// Drive's resumable upload (so a failed chunk can be re-read and retried),
// so this needs zero changes to the RemoteStore interface.
func (p *Pool) uploadChunk(ctx context.Context, acc *Account, folders *folderResolver, fileID string, fileKey []byte, idx int, plaintext []byte) (*uploadedChunk, error) {
	payload, algo := plaintext, storage.CompressionNone
	if compressed, err := storage.CompressBytes(plaintext); err == nil && len(compressed) < len(plaintext) {
		payload, algo = compressed, storage.CompressionFlate
	}

	var ciphertext bytes.Buffer
	md := md5.New()
	if _, err := storage.CopyEncrypt(fileKey, bytes.NewReader(payload), io.MultiWriter(&ciphertext, md)); err != nil {
		return nil, fmt.Errorf("drivepool: encrypt chunk %d: %w", idx, err)
	}
	plaintextSum := sha256.Sum256(plaintext)
	plaintextHash := hex.EncodeToString(plaintextSum[:])
	ciphertextMD5 := hex.EncodeToString(md.Sum(nil))

	folderID, err := folders.resolve(ctx, acc, fileID)
	if err != nil {
		return nil, fmt.Errorf("drivepool: ensure remote folder for chunk %d: %w", idx, err)
	}

	remoteName := fmt.Sprintf("chunk-%04d", idx)
	remoteFileID, remoteMD5, err := acc.Store.Upload(ctx, remoteName, folderID, bytes.NewReader(ciphertext.Bytes()), int64(ciphertext.Len()))
	if err != nil {
		return nil, fmt.Errorf("drivepool: upload chunk %d: %w", idx, err)
	}
	if remoteMD5 != "" && remoteMD5 != ciphertextMD5 {
		return nil, fmt.Errorf("drivepool: chunk %d integrity check failed: local md5 %s != remote md5 %s", idx, ciphertextMD5, remoteMD5)
	}

	return &uploadedChunk{
		index:           idx,
		accountID:       acc.ID,
		remoteFileID:    remoteFileID,
		remoteFolderID:  folderID,
		plaintextSize:   int64(len(plaintext)),
		plaintextSHA256: plaintextHash,
		ciphertextMD5:   ciphertextMD5,
		compressionAlgo: algo,
		compressedSize:  int64(len(payload)),
	}, nil
}

// cleanupChunks best-effort deletes chunks that were already uploaded when
// a later chunk in the same Put failed, so a partial failure doesn't leave
// orphaned ciphertext on some accounts with no manifest row pointing to
// it. A cleanup failure is logged, not returned — it must never mask the
// original error.
func (p *Pool) cleanupChunks(ctx context.Context, chunks []*uploadedChunk) {
	for _, c := range chunks {
		acc, ok := p.accounts[c.accountID]
		if !ok || acc.initErr != nil {
			continue
		}
		if err := acc.Store.Delete(ctx, c.remoteFileID); err != nil {
			log.Printf("drivepool: cleanup: failed to delete orphaned chunk %d (%s): %v", c.index, c.remoteFileID, err)
		}
	}
}

// Get downloads virtualName's chunks, decrypts them, verifies the
// plaintext against the recorded content hash, and writes the result to
// destPath. It is a thin wrapper around GetStream so the CLI's
// write-to-a-local-file use case needs no changes.
func (p *Pool) Get(ctx context.Context, virtualName, destPath string) error {
	dst, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("drivepool: create %q: %w", destPath, err)
	}
	defer dst.Close()

	return p.GetStream(ctx, virtualName, dst)
}

// GetStream downloads and decrypts virtualName's chunks and writes the
// plaintext to w in order. w is a plain io.Writer (not io.WriterAt) so
// this can stream straight to an HTTP response, which has no seek — so
// chunks are fetched+decrypted concurrently but emitted strictly in
// order: a slice of one buffered channel per chunk index lets goroutines
// finish out of order while the single writer below drains channel 0,
// then 1, then 2, ...
func (p *Pool) GetStream(ctx context.Context, virtualName string, w io.Writer) error {
	vf, err := p.manifest.GetVirtualFile(ctx, p.userID, virtualName)
	if err != nil {
		return err
	}

	chunks, err := p.manifest.ListChunks(ctx, p.userID, vf.ID)
	if err != nil {
		return err
	}
	if len(chunks) == 0 {
		return fmt.Errorf("drivepool: %q has no recorded chunks", virtualName)
	}

	ready := make([]chan []byte, len(chunks))
	for i := range ready {
		ready[i] = make(chan []byte, 1)
	}

	sem := make(chan struct{}, p.maxParallelUploads())
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for i, chunk := range chunks {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, chunk manifest.Chunk) {
			defer wg.Done()
			defer func() { <-sem }()

			plaintext, err := p.downloadChunk(ctx, vf.FileKey, chunk)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				close(ready[i])
				return
			}
			ready[i] <- plaintext
		}(i, chunk)
	}

	fileHash := sha256.New()
	var writeErr error
	for i := range chunks {
		plaintext, ok := <-ready[i]
		if !ok {
			break // this chunk failed; firstErr is set below, still drain remaining goroutines
		}
		if writeErr == nil {
			if _, err := w.Write(plaintext); err != nil {
				writeErr = err
			} else {
				fileHash.Write(plaintext)
			}
		}
	}
	wg.Wait()

	if firstErr != nil {
		return firstErr
	}
	if writeErr != nil {
		return writeErr
	}
	if got := hex.EncodeToString(fileHash.Sum(nil)); got != vf.ContentHash {
		return fmt.Errorf("drivepool: content hash mismatch for %q: got %s want %s", virtualName, got, vf.ContentHash)
	}
	return nil
}

// downloadChunk downloads and decrypts one chunk, verifying it against its
// own recorded plaintext hash before handing the bytes back.
func (p *Pool) downloadChunk(ctx context.Context, fileKey []byte, chunk manifest.Chunk) ([]byte, error) {
	if chunk.AccountID == nil {
		return nil, fmt.Errorf("drivepool: chunk %d's account is unavailable", chunk.Index)
	}
	a, ok := p.accounts[*chunk.AccountID]
	if !ok || a.initErr != nil {
		return nil, fmt.Errorf("drivepool: chunk %d's account is unavailable", chunk.Index)
	}

	rc, err := a.Store.Download(ctx, chunk.RemoteFileID)
	if err != nil {
		return nil, fmt.Errorf("drivepool: download chunk %d: %w", chunk.Index, err)
	}
	defer rc.Close()

	var decrypted bytes.Buffer
	if _, err := storage.CopyDecrypt(fileKey, rc, &decrypted); err != nil {
		return nil, fmt.Errorf("drivepool: decrypt chunk %d: %w", chunk.Index, err)
	}

	plaintext := decrypted.Bytes()
	if chunk.CompressionAlgo == storage.CompressionFlate {
		out, err := storage.DecompressBytes(plaintext)
		if err != nil {
			return nil, fmt.Errorf("drivepool: decompress chunk %d: %w", chunk.Index, err)
		}
		plaintext = out
	}

	sum := sha256.Sum256(plaintext)
	if got := hex.EncodeToString(sum[:]); got != chunk.PlaintextSHA256 {
		return nil, fmt.Errorf("drivepool: chunk %d hash mismatch: got %s want %s", chunk.Index, got, chunk.PlaintextSHA256)
	}
	return plaintext, nil
}

func (p *Pool) List(ctx context.Context, prefix string) ([]manifest.VirtualFile, error) {
	return p.manifest.ListVirtualFiles(ctx, p.userID, prefix)
}

// FileInfo is a virtual file plus the labels of every account currently
// holding at least one of its chunks — used by the web portal to show
// where a file's data actually lives, since a large file's chunks can be
// spread across several accounts (see PutStream, §1.2).
type FileInfo struct {
	manifest.VirtualFile
	Accounts []string
	// Degraded is true if at least one chunk's account has been
	// disconnected — the file may be partially or fully unrecoverable
	// until that account (or one holding the same data) is reconnected.
	Degraded bool
}

// ListWithAccounts is List plus, for each file, the deduplicated labels of
// every account holding one of its chunks. Not used by the CLI (ls stays
// on the plain List above) — kept separate so List's existing behavior
// and callers are untouched.
func (p *Pool) ListWithAccounts(ctx context.Context, prefix string) ([]FileInfo, error) {
	files, err := p.manifest.ListVirtualFiles(ctx, p.userID, prefix)
	if err != nil {
		return nil, err
	}

	out := make([]FileInfo, 0, len(files))
	for _, f := range files {
		chunks, err := p.manifest.ListChunks(ctx, p.userID, f.ID)
		if err != nil {
			return nil, err
		}

		seen := make(map[string]bool, len(chunks))
		labels := make([]string, 0, len(chunks))
		degraded := false
		for _, c := range chunks {
			key := "" // dedup key; every disconnected chunk groups under this one entry
			if c.AccountID != nil {
				key = *c.AccountID
			}
			if seen[key] {
				continue
			}
			seen[key] = true

			var a *Account
			if c.AccountID != nil {
				a = p.accounts[*c.AccountID]
			}
			if a == nil {
				degraded = true
				labels = append(labels, "disconnected")
				continue
			}
			labels = append(labels, a.Label)
		}

		out = append(out, FileInfo{VirtualFile: f, Accounts: labels, Degraded: degraded})
	}
	return out, nil
}

func (p *Pool) Remove(ctx context.Context, virtualName string, purge bool) error {
	if purge {
		vf, err := p.manifest.GetVirtualFile(ctx, p.userID, virtualName)
		if err != nil {
			return err
		}
		chunks, err := p.manifest.ListChunks(ctx, p.userID, vf.ID)
		if err != nil {
			return err
		}

		// folders collects each healthy account's per-file folder
		// (accountID -> folderID), deduplicated — every chunk an account
		// holds for this file lives in the one folder uploadChunk created
		// for it (see folderResolver), so it only needs deleting once,
		// after all of that account's chunks are gone.
		folders := make(map[string]string)
		for _, chunk := range chunks {
			if chunk.AccountID == nil {
				continue
			}
			a, ok := p.accounts[*chunk.AccountID]
			if !ok || a.initErr != nil {
				continue
			}
			// A chunk (or folder, below) deleted directly in Drive's own
			// UI (outside this app) before the user clicked delete here is
			// already in the state we want — treat that as success rather
			// than blocking the rest of the purge and leaving the manifest
			// row (and every other chunk) stuck undeleted forever.
			if err := a.Store.Delete(ctx, chunk.RemoteFileID); err != nil && !errors.Is(err, gdrive.ErrNotFound) {
				return fmt.Errorf("drivepool: delete remote chunk %d: %w", chunk.Index, err)
			}
			folders[*chunk.AccountID] = chunk.RemoteFolderID
		}

		// Drive has no separate "directory" type — a folder is just
		// another file ID — so the same Delete call used for chunks above
		// deletes a now-empty per-file folder too, instead of leaving it
		// behind as clutter.
		for accountID, folderID := range folders {
			if err := p.accounts[accountID].Store.Delete(ctx, folderID); err != nil && !errors.Is(err, gdrive.ErrNotFound) {
				return fmt.Errorf("drivepool: delete remote folder %s: %w", folderID, err)
			}
		}
	}
	return p.manifest.DeleteVirtualFile(ctx, p.userID, virtualName, purge)
}
