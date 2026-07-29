// Package drivepool pools multiple Google Drive accounts into one virtual
// storage volume: registered accounts are placement candidates, files are
// encrypted and uploaded to whichever account currently has the most free
// space, and a local SQLite manifest maps virtual file names back to the
// remote chunks that hold their bytes.
//
// Splitting large files into many chunks spread across accounts (rather
// than the current one-chunk-per-file behavior) is tracked as future work —
// see tasks/modification_plan.md, Phase 3.
package drivepool

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Souvik-223/rhino-framework/drivepool/auth"
	"github.com/Souvik-223/rhino-framework/drivepool/gdrive"
	"github.com/Souvik-223/rhino-framework/drivepool/manifest"
	"github.com/Souvik-223/rhino-framework/storage"
	"golang.org/x/oauth2"
)

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

// AddAccount runs the OAuth consent flow for a new account label, persists
// its token, queries its starting quota, and registers it in the manifest.
func (p *Pool) AddAccount(ctx context.Context, label string) (*Account, error) {
	if _, ok := p.accounts[label]; ok {
		return nil, fmt.Errorf("drivepool: account label %q already registered", label)
	}

	tok, err := auth.RunConsentFlow(ctx, p.clientCfg)
	if err != nil {
		return nil, fmt.Errorf("drivepool: consent flow: %w", err)
	}
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

// Put encrypts localPath and uploads it to whichever registered account
// currently has the most free space, recording the result as a single-chunk
// virtual file named virtualName.
func (p *Pool) Put(ctx context.Context, localPath, virtualName string) error {
	src, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("drivepool: open %q: %w", localPath, err)
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		return fmt.Errorf("drivepool: stat %q: %w", localPath, err)
	}

	staged, err := os.CreateTemp("", "rhino-chunk-*")
	if err != nil {
		return fmt.Errorf("drivepool: create staging file: %w", err)
	}
	stagedPath := staged.Name()
	defer os.Remove(stagedPath)
	defer staged.Close()

	fileKey := storage.NewEncryptionKey()
	sha := sha256.New()
	md := md5.New()

	teedSrc := io.TeeReader(src, sha)
	ciphertextWriter := io.MultiWriter(staged, md)
	if _, err := storage.CopyEncrypt(fileKey, teedSrc, ciphertextWriter); err != nil {
		return fmt.Errorf("drivepool: encrypt: %w", err)
	}

	stagedInfo, err := staged.Stat()
	if err != nil {
		return fmt.Errorf("drivepool: stat staged chunk: %w", err)
	}
	contentHash := hex.EncodeToString(sha.Sum(nil))
	localCiphertextMD5 := hex.EncodeToString(md.Sum(nil))

	candidates := make([]*Account, 0, len(p.accounts))
	for _, a := range p.accounts {
		candidates = append(candidates, a)
	}
	target, err := pickAccount(ctx, candidates)
	if err != nil {
		return err
	}

	folderID, err := target.Store.EnsureFolder(ctx, contentHash)
	if err != nil {
		return fmt.Errorf("drivepool: ensure remote folder: %w", err)
	}

	if _, err := staged.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("drivepool: rewind staged chunk: %w", err)
	}
	remoteFileID, remoteMD5, err := target.Store.Upload(ctx, contentHash, folderID, staged, stagedInfo.Size())
	if err != nil {
		return fmt.Errorf("drivepool: upload: %w", err)
	}
	if remoteMD5 != "" && remoteMD5 != localCiphertextMD5 {
		return fmt.Errorf("drivepool: upload integrity check failed: local md5 %s != remote md5 %s", localCiphertextMD5, remoteMD5)
	}

	vf := &manifest.VirtualFile{
		Name:        virtualName,
		Size:        info.Size(),
		ContentHash: contentHash,
		ChunkSize:   info.Size(),
		FileKey:     fileKey,
		Status:      manifest.StatusIncomplete,
	}
	if err := p.manifest.CreateVirtualFile(ctx, vf); err != nil {
		return fmt.Errorf("drivepool: record virtual file: %w", err)
	}

	if err := p.manifest.AddChunk(ctx, &manifest.Chunk{
		VirtualFileID:   vf.ID,
		Index:           0,
		AccountID:       target.ID,
		RemoteFileID:    remoteFileID,
		RemoteFolderID:  folderID,
		PlaintextSize:   info.Size(),
		PlaintextSHA256: contentHash,
		CiphertextMD5:   localCiphertextMD5,
	}); err != nil {
		return fmt.Errorf("drivepool: record chunk: %w", err)
	}

	return p.manifest.SetVirtualFileStatus(ctx, vf.ID, manifest.StatusComplete)
}

// Get downloads virtualName's chunks (currently always exactly one),
// decrypts them, verifies the plaintext against the recorded content hash,
// and writes the result to destPath.
func (p *Pool) Get(ctx context.Context, virtualName, destPath string) error {
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

	dst, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("drivepool: create %q: %w", destPath, err)
	}
	defer dst.Close()

	sha := sha256.New()

	for _, chunk := range chunks {
		a, ok := p.accounts[chunk.AccountID]
		if !ok || a.initErr != nil {
			return fmt.Errorf("drivepool: chunk %d's account is unavailable", chunk.Index)
		}

		rc, err := a.Store.Download(ctx, chunk.RemoteFileID)
		if err != nil {
			return fmt.Errorf("drivepool: download chunk %d: %w", chunk.Index, err)
		}

		_, err = storage.CopyDecrypt(vf.FileKey, rc, io.MultiWriter(dst, sha))
		rc.Close()
		if err != nil {
			return fmt.Errorf("drivepool: decrypt chunk %d: %w", chunk.Index, err)
		}
	}

	if got := hex.EncodeToString(sha.Sum(nil)); got != vf.ContentHash {
		return fmt.Errorf("drivepool: content hash mismatch for %q: got %s want %s", virtualName, got, vf.ContentHash)
	}
	return nil
}

func (p *Pool) List(ctx context.Context, prefix string) ([]manifest.VirtualFile, error) {
	return p.manifest.ListVirtualFiles(ctx, prefix)
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
