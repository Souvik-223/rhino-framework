// Package drivepool pools multiple Google Drive accounts into one virtual
// storage volume: registered accounts are placement candidates, files are
// encrypted, split into fixed-size chunks, and each chunk is uploaded to
// whichever account currently has the most free space, and a local SQLite
// manifest maps virtual file names back to the remote chunks that hold
// their bytes.
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

// DefaultChunkSize is used when Pool.ChunkSize is unset (zero).
const DefaultChunkSize = 128 << 20 // 128 MiB

// defaultMaxParallelUploads is used when Pool.MaxParallelUploads is unset.
const defaultMaxParallelUploads = 4

// Account is a registered Drive account: a placement candidate for Put and
// the source for Get. initErr is set when the account's client couldn't be
// built (e.g. a revoked/expired token) — such accounts are skipped by
// placement and reported unhealthy by ListAccounts, never crash an
// operation outright.
type Account struct {
	ID      string
	Label   string
	Store   gdrive.RemoteStore
	initErr error
}

type Pool struct {
	manifest  *manifest.Manifest
	tokens    *auth.TokenStore
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

// Open loads every account already registered in m and builds a Drive
// client for each. An account whose token can't be loaded or refreshed is
// kept in the pool with initErr set, rather than failing Open entirely.
func Open(ctx context.Context, m *manifest.Manifest, tokens *auth.TokenStore, clientCfg *oauth2.Config) (*Pool, error) {
	p := &Pool{
		manifest:  m,
		tokens:    tokens,
		clientCfg: clientCfg,
		newClient: func(ctx context.Context, ts oauth2.TokenSource) (gdrive.RemoteStore, error) {
			return gdrive.NewClient(ctx, ts)
		},
		accounts: make(map[string]*Account),
	}

	rows, err := m.ListAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("drivepool: list accounts: %w", err)
	}

	for _, row := range rows {
		p.accounts[row.ID] = p.buildAccount(ctx, row.ID, row.Label)
	}

	return p, nil
}

// Close releases the pool's underlying manifest database handle.
func (p *Pool) Close() error {
	return p.manifest.Close()
}

func (p *Pool) buildAccount(ctx context.Context, id, label string) *Account {
	a := &Account{ID: id, Label: label}

	tok, err := p.tokens.Load(label)
	if err != nil {
		a.initErr = fmt.Errorf("load token: %w", err)
		return a
	}

	ts := p.clientCfg.TokenSource(ctx, tok)
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
// persists its token, queries its starting quota, and registers it in the
// manifest. This only works when the caller and the browser completing
// consent are the same machine — see AuthCodeURL/CompleteConsent for the
// web-redirect equivalent a remote HTTP backend needs instead.
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
	return cfg.AuthCodeURL(state, oauth2.AccessTypeOffline)
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
	_, err := p.manifest.GetAccount(ctx, label)
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
	if err := p.tokens.Save(label, tok); err != nil {
		return nil, fmt.Errorf("drivepool: save token: %w", err)
	}

	ts := p.clientCfg.TokenSource(ctx, tok)
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
	if err := p.manifest.AddAccount(ctx, manifest.Account{
		ID:        id,
		Label:     label,
		TokenPath: p.tokens.Path(label),
		AddedAt:   now,
	}); err != nil {
		return nil, fmt.Errorf("drivepool: register account: %w", err)
	}
	if err := p.manifest.UpdateAccountQuota(ctx, id, quota.Limit, quota.Usage, now); err != nil {
		return nil, fmt.Errorf("drivepool: record quota: %w", err)
	}

	a := &Account{ID: id, Label: label, Store: store}
	p.accounts[id] = a
	return a, nil
}

func (p *Pool) RemoveAccount(ctx context.Context, label string) error {
	row, err := p.manifest.GetAccount(ctx, label)
	if err != nil {
		return err
	}
	if err := p.manifest.RemoveAccount(ctx, label); err != nil {
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
	Err       error // set if this account's quota couldn't be refreshed
}

func (p *Pool) ListAccountStatus(ctx context.Context) ([]AccountStatus, error) {
	rows, err := p.manifest.ListAccounts(ctx)
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
	if err := p.manifest.CreateVirtualFile(ctx, vf); err != nil {
		p.cleanupChunks(ctx, uploaded)
		return fmt.Errorf("drivepool: record virtual file: %w", err)
	}

	for _, uc := range uploaded {
		if err := p.manifest.AddChunk(ctx, &manifest.Chunk{
			VirtualFileID:   vf.ID,
			Index:           uc.index,
			AccountID:       uc.accountID,
			RemoteFileID:    uc.remoteFileID,
			RemoteFolderID:  uc.remoteFolderID,
			PlaintextSize:   uc.plaintextSize,
			PlaintextSHA256: uc.plaintextSHA256,
			CiphertextMD5:   uc.ciphertextMD5,
		}); err != nil {
			return fmt.Errorf("drivepool: record chunk %d: %w", uc.index, err)
		}
	}

	return p.manifest.SetVirtualFileStatus(ctx, vf.ID, manifest.StatusComplete)
}

// uploadChunk encrypts one chunk's plaintext entirely in memory (no local
// temp file) and uploads it to acc. The resulting *bytes.Reader satisfies
// the same io.ReaderAt gdrive.RemoteStore.Upload already requires for
// Drive's resumable upload (so a failed chunk can be re-read and retried),
// so this needs zero changes to the RemoteStore interface.
func (p *Pool) uploadChunk(ctx context.Context, acc *Account, folders *folderResolver, fileID string, fileKey []byte, idx int, plaintext []byte) (*uploadedChunk, error) {
	var ciphertext bytes.Buffer
	md := md5.New()
	if _, err := storage.CopyEncrypt(fileKey, bytes.NewReader(plaintext), io.MultiWriter(&ciphertext, md)); err != nil {
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
	vf, err := p.manifest.GetVirtualFile(ctx, virtualName)
	if err != nil {
		return err
	}

	chunks, err := p.manifest.ListChunks(ctx, vf.ID)
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
	a, ok := p.accounts[chunk.AccountID]
	if !ok || a.initErr != nil {
		return nil, fmt.Errorf("drivepool: chunk %d's account is unavailable", chunk.Index)
	}

	rc, err := a.Store.Download(ctx, chunk.RemoteFileID)
	if err != nil {
		return nil, fmt.Errorf("drivepool: download chunk %d: %w", chunk.Index, err)
	}
	defer rc.Close()

	var plaintext bytes.Buffer
	if _, err := storage.CopyDecrypt(fileKey, rc, &plaintext); err != nil {
		return nil, fmt.Errorf("drivepool: decrypt chunk %d: %w", chunk.Index, err)
	}

	sum := sha256.Sum256(plaintext.Bytes())
	if got := hex.EncodeToString(sum[:]); got != chunk.PlaintextSHA256 {
		return nil, fmt.Errorf("drivepool: chunk %d hash mismatch: got %s want %s", chunk.Index, got, chunk.PlaintextSHA256)
	}
	return plaintext.Bytes(), nil
}

func (p *Pool) List(ctx context.Context, prefix string) ([]manifest.VirtualFile, error) {
	return p.manifest.ListVirtualFiles(ctx, prefix)
}

// FileInfo is a virtual file plus the labels of every account currently
// holding at least one of its chunks — used by the web portal to show
// where a file's data actually lives, since a large file's chunks can be
// spread across several accounts (see PutStream, §1.2).
type FileInfo struct {
	manifest.VirtualFile
	Accounts []string
}

// ListWithAccounts is List plus, for each file, the deduplicated labels of
// every account holding one of its chunks. Not used by the CLI (ls stays
// on the plain List above) — kept separate so List's existing behavior
// and callers are untouched.
func (p *Pool) ListWithAccounts(ctx context.Context, prefix string) ([]FileInfo, error) {
	files, err := p.manifest.ListVirtualFiles(ctx, prefix)
	if err != nil {
		return nil, err
	}

	out := make([]FileInfo, 0, len(files))
	for _, f := range files {
		chunks, err := p.manifest.ListChunks(ctx, f.ID)
		if err != nil {
			return nil, err
		}

		seen := make(map[string]bool, len(chunks))
		labels := make([]string, 0, len(chunks))
		for _, c := range chunks {
			if seen[c.AccountID] {
				continue
			}
			seen[c.AccountID] = true

			label := c.AccountID // fall back to the raw ID if the account was since removed
			if a, ok := p.accounts[c.AccountID]; ok {
				label = a.Label
			}
			labels = append(labels, label)
		}

		out = append(out, FileInfo{VirtualFile: f, Accounts: labels})
	}
	return out, nil
}

func (p *Pool) Remove(ctx context.Context, virtualName string, purge bool) error {
	if purge {
		vf, err := p.manifest.GetVirtualFile(ctx, virtualName)
		if err != nil {
			return err
		}
		chunks, err := p.manifest.ListChunks(ctx, vf.ID)
		if err != nil {
			return err
		}
		for _, chunk := range chunks {
			if a, ok := p.accounts[chunk.AccountID]; ok && a.initErr == nil {
				if err := a.Store.Delete(ctx, chunk.RemoteFileID); err != nil {
					return fmt.Errorf("drivepool: delete remote chunk %d: %w", chunk.Index, err)
				}
			}
		}
	}
	return p.manifest.DeleteVirtualFile(ctx, virtualName, purge)
}
