# Web Portal Implementation Plan

> **Note (post-migration):** §1 below ("Why per-user Pool instances, not a
> shared-schema rewrite") describes the *original* design — one SQLite file
> per portal user — and its "going with (B)" conclusion. That decision was
> later reversed: the project moved to option (A), a single shared Postgres
> database with every table scoped by `user_id`. See
> [`er_diagram.md`](er_diagram.md) for the current schema and
> `drivepool/manifest/manifest.go`'s `scoped()` for how tenant isolation
> works now. The rest of this document (frontend structure, Gin routes,
> Docker/CI setup, chunking design in §1.2) is unaffected by that change and
> still describes the real, current implementation.

Adds a browser-based, Google-Drive-like UI on top of `drivepool` (drag-drop
upload, per-file download/delete, a sidebar showing every connected Drive
account and its fill level) **without changing any existing CLI behavior**.
`rhino put/get/ls/rm/account/status` stay exactly as they are; the portal is
a new `rhino serve` subcommand of the same binary.

Decisions locked in for this plan (see "Open questions" at the bottom for
what's still a judgment call):
- Frontend: **Vue 3 + Vite** SPA, built to static assets and embedded into
  the Go binary — lives in its own top-level `frontend/` directory.
- Backend HTTP layer: **Gin**, not stdlib `net/http` — lives in its own
  top-level `backend/` directory (renamed from an earlier draft's `webui/`,
  which read too much like "frontend" and didn't describe what the package
  actually is).
- Deployment: reachable over a network, not just localhost.
- Tenancy: **real multi-tenancy** — each logged-in user connects and pools
  their own Drive accounts; users never see each other's accounts or files.
- Shared logic between the CLI and the backend lives in `drivepool` itself
  (§1.1) — both are thin callers of the same bootstrap functions, not two
  copies of the same logic.
- **Multi-account chunking is now in scope** (§1.2) — real file-splitting
  across accounts, pulled forward from `tasks/modification_plan.md`'s
  Phase 3, rather than deferred. This is the single biggest change in this
  plan and revises several statements in earlier drafts that assumed
  `Pool.Put`/`Pool.Get` stayed completely untouched.
- Existing `drivepool`/`p2p`/`storage` stay at their current top-level
  locations — no `internal/`/`pkg/` reorganization of already-working code.

---

## 1. Why per-user Pool instances, not a shared-schema rewrite

`drivepool.Pool`/`manifest` are already fully self-contained per "installation"
(one manifest DB + one token directory + one set of in-memory `Account`
clients, all wired up once in `openPool()` in
[cmd/rhino/main.go](../cmd/rhino/main.go)). Two ways to get multi-tenancy:

- **(A) Rewrite for shared state**: add `user_id` to the `accounts` and
  `virtual_files` tables, thread a user ID through every `Pool`/`Manifest`
  method, and add per-user scoping everywhere. Touches the core, tested
  package (`drivepool/pool_test.go`) and every query in
  `drivepool/manifest/`.
- **(B) One Pool per user, reusing today's code untouched**: give every user
  their own manifest DB file and their own token directory (same layout the
  CLI already uses at `%APPDATA%\rhino\`, just nested one level under a user
  ID), and let the backend hold a small cache of `*drivepool.Pool` instances
  keyed by user ID, opened lazily on first request.

**Going with (B).** The `Pool`/`Manifest` Go API stays identical to today —
the entire existing test suite and CLI keep working unmodified. What's new
is *who calls that API and from which directory*, which is exactly what
§1.1 below shares between the CLI and the backend.

### 1.1 Shared bootstrap library: extend `drivepool`, don't duplicate it

Today, `cmd/rhino/main.go`'s `configDir()` + `openPool()` inline the whole
"resolve a directory, build a manifest/token-store/OAuth-config, open a
Pool" sequence. The backend needs to do the *exact same sequence*, once per
user directory instead of once globally. Rather than the backend
reimplementing that bootstrap independently (two copies of the same logic
to keep in sync), it's pulled out into two new exported functions in
`drivepool` itself — the package already imported by both the CLI and the
backend, so this isn't a new dependency for either side:

```go
// drivepool/bootstrap.go

// ResolveDataDir returns the directory rhino should read/write its
// manifest, tokens, and OAuth client config from. override (typically
// os.Getenv("RHINO_DATA_DIR")) wins if set; otherwise falls back to
// os.UserConfigDir()/rhino, matching today's CLI-only behavior exactly.
func ResolveDataDir(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("drivepool: resolve config dir: %w", err)
	}
	return filepath.Join(dir, "rhino"), nil
}

// OpenFromDataDir wires up the manifest, token store, and OAuth client
// config found under dataDir and opens a Pool. credentialsPath overrides
// where client_secret.json is read from — pass "" to default to
// dataDir/client_secret.json (the CLI's layout), or an explicit shared path
// (the backend's layout, since one OAuth app credential is shared across
// every user's per-user dataDir).
func OpenFromDataDir(ctx context.Context, dataDir, credentialsPath string, scopes ...string) (*Pool, error) {
	m, err := manifest.Open(filepath.Join(dataDir, "manifest.db"))
	if err != nil {
		return nil, fmt.Errorf("drivepool: open manifest: %w", err)
	}
	tokens := auth.NewTokenStore(filepath.Join(dataDir, "accounts"))

	if credentialsPath == "" {
		credentialsPath = filepath.Join(dataDir, "client_secret.json")
	}
	clientCfg, err := auth.LoadClientConfig(credentialsPath, scopes...)
	if err != nil {
		m.Close()
		return nil, fmt.Errorf("drivepool: load OAuth client config: %w", err)
	}

	return Open(ctx, m, tokens, clientCfg)
}
```

Both entrypoints become thin callers:

```go
// cmd/rhino/main.go — MODIFIED, openPool() shrinks to:
func openPool(ctx context.Context) (*drivepool.Pool, error) {
	dataDir, err := drivepool.ResolveDataDir(os.Getenv("RHINO_DATA_DIR"))
	if err != nil {
		return nil, err
	}
	return drivepool.OpenFromDataDir(ctx, dataDir, credentialsPath, gdrive.DriveFileScope)
}
```

```go
// backend/poolcache.go — per-user pools, same function, different directory:
func (c *poolCache) get(ctx context.Context, userID string) (*drivepool.Pool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p, ok := c.pools[userID]; ok {
		return p, nil
	}
	dataDir := filepath.Join(c.baseDataDir, "users", userID)
	p, err := drivepool.OpenFromDataDir(ctx, dataDir, c.sharedClientSecretPath, gdrive.DriveFileScope)
	if err != nil {
		return nil, err
	}
	c.pools[userID] = p
	return p, nil
}
```

Neither `cmd/rhino` nor `backend` knows *how* a Pool gets built — that's
entirely `drivepool`'s job, in one place. A background ticker in
`poolCache` closes (`Pool.Close()`) and evicts entries idle for longer than
e.g. 30 minutes — `manifest.Manifest` already restricts itself to
`SetMaxOpenConns(1)`, so holding many users' DBs open indefinitely would
just accumulate file handles for no benefit.

### 1.2 Real multi-account chunking (Phase 3, pulled forward)

Per your call, this plan now includes actually splitting one file's bytes
across multiple Drive accounts — the thing `tasks/modification_plan.md` has
tracked as future work, and that CLAUDE.md explicitly says isn't built yet
today (`Pool.Put` still uploads the whole encrypted file as a single chunk
at `idx = 0`). This is the biggest change in this plan; it revises several
"no core changes needed" statements made earlier for the backend routes.

**What stays the same, what changes**: `Put(ctx, localPath, virtualName) error`
and `Get(ctx, virtualName, destPath) error` keep their *exact* signatures —
the CLI needs zero changes, matching "keep the CLI features." Internally,
both become thin wrappers around two new exported methods that do the real
work, which the backend calls directly instead of going through a local
path/file:

```go
func (p *Pool) Put(ctx context.Context, localPath, virtualName string) error {
	f, err := os.Open(localPath)
	if err != nil { return err }
	defer f.Close()
	info, err := f.Stat()
	if err != nil { return err }
	return p.PutStream(ctx, f, info.Size(), virtualName)
}

func (p *Pool) Get(ctx context.Context, virtualName, destPath string) error {
	dst, err := os.Create(destPath)
	if err != nil { return err }
	defer dst.Close()
	return p.GetStream(ctx, virtualName, dst) // *os.File satisfies io.Writer
}
```

`PutStream(ctx, r io.Reader, size int64, virtualName string) error` and
`GetStream(ctx, virtualName string, w io.Writer) error` are what the
backend's upload/download handlers call directly — `r` is the multipart
file part, `w` is `c.Writer` (Gin's `http.ResponseWriter`, a plain
`io.Writer`). **No local temp file on the web path at all** — see
"in-memory buffering" below.

**Chunk boundaries**: fixed-size, not account-capacity-driven — a
`ChunkSize` (default e.g. 128MiB, configurable) split of the input, computed
purely from total size. Tying chunk boundaries to "however much room is
left on this account" would make chunk sizes non-deterministic and
complicate retry; fixed-size chunks match how the schema already models
things (`chunks.idx`, `chunks.plaintext_size` per row). Files smaller than
one `ChunkSize` take exactly the path `Put` already takes today (one chunk,
one `pickAccount` call, unchanged) — the new machinery below only engages
once a file actually needs more than one chunk.

**Placement across many chunks, concurrently, without hammering the Drive
API**: `pickAccount` (`drivepool/placement.go`) deliberately re-queries live
quota on *every* call today — correct for "once per whole file," wrong for
"once per chunk, from several goroutines at once" (redundant network
round-trips, plus a real race: two goroutines could both see the same
account as "most free" before either upload actually registers). New
addition, `pickAccount` itself is untouched (still used as-is for the
single-chunk case):

```go
// drivepool/placement.go — new addition alongside pickAccount
type placementTracker struct {
	mu        sync.Mutex
	available map[string]int64 // accountID -> live Available() at PutStream start,
	                            // decremented in-memory as chunks are assigned
}

func newPlacementTracker(ctx context.Context, accounts []*Account) (*placementTracker, error) {
	// one live Quota() call per healthy account — same query pickAccount
	// already does, just once for the whole PutStream call, not per chunk
}

func (t *placementTracker) reserve(size int64) (accountID string, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// pick the account with the largest remaining `available`, decrement
	// it by size, return its ID — pure in-memory, no network call
}
```

**Encryption stays exactly `storage.CopyEncrypt`, called once per chunk**:
`virtual_files.file_key` is still one key per virtual file; CTR mode only
requires a unique IV per encryption, and `CopyEncrypt` already generates a
fresh random IV per call — so calling it once per chunk with the same
`fileKey` is correct as-is, not a crypto change, just calling an existing
primitive more than once per `Put`.

**In-memory buffering, not disk staging** — the actual fix for the
disk-I/O concern, without giving up retry-safety: each chunk is read from
`r` into a bounded buffer, encrypted with `storage.CopyEncrypt` into a
`bytes.Buffer`, then wrapped as `bytes.NewReader(buf.Bytes())`.
`*bytes.Reader` already implements `io.ReaderAt` — the exact interface
`gdrive.RemoteStore.Upload` already requires for Drive's resumable upload
(needed precisely so a failed chunk can be re-read and retried without
restarting the whole file from byte zero). **Zero changes needed to the
`RemoteStore` interface or `gdrive.Client`** — swapping "read from a staged
local file" for "read from an in-memory buffer" is a drop-in change at the
call site, since both already satisfy the same `io.ReaderAt` contract.
Peak memory is bounded by `ChunkSize × maxParallelUploads` (both
configurable) — e.g. 128MiB × 4 = 512MiB worst case per concurrent `Put`,
tunable per deployment via `backend/config.go`'s env vars.

**Concurrent chunk uploads**, matching this repo's existing preference for
explicit `sync.WaitGroup`/mutex concurrency (`FileServer.peerLock`) over a
third-party concurrency library — no new Go dependency needed, `sync`/
`bytes`/`io` are all stdlib:

```go
sem := make(chan struct{}, maxParallelUploads) // bounded worker pool
var wg sync.WaitGroup
var mu sync.Mutex
var firstErr error

for idx, chunk := range encryptedChunks {
	wg.Add(1)
	sem <- struct{}{}
	go func(idx int, chunk []byte) {
		defer wg.Done()
		defer func() { <-sem }()
		if err := uploadOneChunk(ctx, tracker, idx, chunk); err != nil {
			mu.Lock()
			if firstErr == nil {
				firstErr = err
			}
			mu.Unlock()
		}
	}(idx, chunk)
}
wg.Wait()
if firstErr != nil {
	return firstErr // + best-effort cleanup, below
}
```

**Download**: `GetStream`'s target is a plain `io.Writer` (an HTTP response
has no seek/`WriteAt`), so chunks are fetched+decrypted concurrently but
must be *emitted* strictly in order. A slice of one buffered channel per
chunk index keeps this simple — goroutines can finish out of order, the
single writer just drains channel 0, then 1, then 2, ...:

```go
chunkReady := make([]chan []byte, len(chunks))
for i := range chunkReady {
	chunkReady[i] = make(chan []byte, 1)
}
// bounded goroutines (same sem pattern) download+decrypt chunk i, verify
// it against chunks[i].PlaintextSHA256, then chunkReady[i] <- plaintext

for i := range chunks {
	plaintext := <-chunkReady[i] // blocks until chunk i specifically is ready
	if _, err := w.Write(plaintext); err != nil {
		return err
	}
}
```

**Partial-failure handling**: if any chunk fails after others already
uploaded, best-effort `Store.Delete` the chunks that did succeed (log if
that cleanup itself fails, don't mask the original error) rather than
leaving orphaned ciphertext on some accounts with no manifest row pointing
to it — the same graceful-degradation spirit already documented for
`drivepool.Account.initErr`, applied to a new failure mode.
**Explicitly still out of scope**: `chunk_replicas` (replicating a chunk to
more than one account for redundancy) — the schema already has the table,
this plan still doesn't populate it. One account going down still fails
that file's download; replication stays a further, separate future step,
not part of this pull-forward.

**Side benefit**: chunk completion is a natural, free progress signal — "3
of 8 chunks done" needs no byte-level callback plumbing, just an event
emitted from the upload loop above. This **upgrades §6 phase 5's progress
bar from "needs a core-package change" to "already has the hooks it
needs"** once chunking lands — coarser than byte-level (a chunk is
tens-to-hundreds of MB), but real, and it's the natural unit `PutStream`
already works in.

**Manifest/schema impact**: none. `chunks` already has `idx`,
`plaintext_size`, `plaintext_sha256`, `account_id`, `remote_file_id`,
`remote_folder_id` — one row per chunk, exactly what today's schema
already models. `PutStream` just writes `N` rows instead of always writing
exactly one row at `idx = 0`. No new migration needed for this part.

**Testing impact**: `drivepool/pool_test.go`'s existing `fakeStore` +
`addFakeAccount` (multiple named fakes with independent `limit`/`usage`)
already model exactly what chunk-placement tests need — multiple accounts
with different free space, verifying chunks land on the right ones and
that a file larger than any single account's free space actually gets
split rather than failing placement outright. New test cases in
`pool_test.go`; no new test infrastructure required.

### Directory layout

```
%APPDATA%\rhino\                      (unchanged for CLI/local single-user use)
├── client_secret.json                 shared OAuth app credential (all users)
├── manifest.db                        untouched — still used by the local CLI
├── accounts\                          untouched — still used by the local CLI
│
├── users.db                           NEW — portal login accounts (see §2)
└── users\                             NEW — one subtree per portal user
    └── <userID>\
        ├── manifest.db                 same schema as today, just per-user
        └── accounts\                   same auth.TokenStore, just per-user
```

The CLI never reads `users.db` or `users/`; the backend never reads the
top-level `manifest.db`/`accounts/`. Two independent trees, same
`drivepool.OpenFromDataDir` underneath.

---

## 2. Backend (`backend/`, Gin)

```
backend/
├── server.go          gin.Engine + route table, Server{cache *poolCache, users *authdb.DB}
├── config.go           reads RHINO_* env vars (§8.2)
├── handlers_auth.go    register/login/logout
├── handlers.go         accounts/files handlers, userID resolved from session middleware
├── handlers_health.go  /healthz, /readyz
├── middleware.go        session-verification gin.HandlerFunc, sets userID in gin.Context
├── poolcache.go         per-user *drivepool.Pool cache (§1.1)
├── assets.go            //go:embed dist — serves the built frontend (see note below)
├── dist/                 git-ignored — frontend/dist copied here before `go build`
│                         (go:embed can only reach subdirectories of its own package,
│                         so frontend/'s build output can't be embedded directly from
│                         a sibling directory — the copy step is required, see §3)
└── authdb/
    └── users.go          tiny SQLite table: users(id, username, password_hash, created_at)
```

`authdb` reuses `modernc.org/sqlite` (already a dependency) rather than
introducing a different storage mechanism just for auth — one table, opened
once at server startup at `%APPDATA%\rhino\users.db`.

### Routes (Gin route groups)

```
POST   /api/auth/register     {username, password} -> create user (bcrypt hash)
POST   /api/auth/login        {username, password} -> verify, set session cookie
POST   /api/auth/logout       -> clear session
GET    /api/me                -> current username

# everything below sits behind an auth-required route group; userID comes
# from the verified session only, never from a client-supplied field/param
GET    /api/accounts
POST   /api/accounts          {label} -> pool.AddAccount (OAuth consent, browser-triggered)
DELETE /api/accounts/:label

GET    /api/files
POST   /api/files              multipart upload -> pool.PutStream (no temp file, §1.2)
GET    /api/files/:name/download   pool.GetStream -> c.Writer (no temp file, §1.2)
DELETE /api/files/:name
```

```go
// backend/server.go sketch
r := gin.New()
r.Use(gin.Logger(), gin.Recovery())

api := r.Group("/api")
api.POST("/auth/register", h.register)
api.POST("/auth/login", h.login)

authed := api.Group("/")
authed.Use(h.requireSession) // sets c.Set("userID", ...) or aborts 401
authed.GET("/accounts", h.listAccounts)
authed.POST("/files", h.uploadFile)
// ...
```

Upload/download handlers call the new `Pool.PutStream`/`Pool.GetStream`
(§1.2) directly — the multipart file part and `c.Writer` respectively — with
**no local temp file on the server at all**, and chunks get spread across
whichever accounts have room via the `placementTracker` (§1.2) rather than
landing entirely on one account the way today's `Put` does.

**Progress in v1**: chunking (§1.2) gives per-chunk completion as a free,
natural progress signal — no byte-level callback plumbing needed, just an
SSE event emitted each time `uploadOneChunk`/a chunk download finishes.
Coarser than byte-level (a chunk is tens-to-hundreds of MB) but real, and
available from day one rather than needing the follow-up core-package
change originally planned for it — see the revised §6 phase list.

### New Go dependencies

- **`github.com/gin-gonic/gin`** — new direct dependency (per your steer
  away from stdlib `net/http`). Pulls its own transitive tree
  (`json-iterator`, `go-playground/validator`, etc.) — worth it here for
  route groups, middleware chaining, and request binding/validation
  helpers that would otherwise be hand-rolled boilerplate across every
  handler.
- **`github.com/gin-contrib/sessions`** (cookie store) — now that Gin is
  already a dependency, this is a better default than the hand-rolled
  HMAC-cookie approach from the previous draft: it's the standard
  session middleware in Gin's own ecosystem (wraps `gorilla/sessions`
  under the hood), well-tested, and removes custom cookie-signing code
  from this project's attack surface. Supersedes the earlier "hand-roll
  it to avoid a dependency" call now that a framework — and its
  ecosystem — is already the chosen direction.
- `golang.org/x/crypto/bcrypt` — **already an indirect dependency**
  (`golang.org/x/crypto v0.54.0` pulled in via oauth2/sqlite), just needs
  to become direct. No new module fetch.

### Tests: `backend/tests/`

Per your steer, backend tests live in their own folder rather than
co-located `_test.go` files next to each handler — with one small, necessary
exception explained below.

```
backend/
├── tests/                     all backend test *cases* live here
│   ├── testutil/
│   │   └── fakestore.go        small in-memory gdrive.RemoteStore (same idea as
│   │                           drivepool/pool_test.go's fakeStore, duplicated
│   │                           here since that one's unexported and this is a
│   │                           different package)
│   ├── auth_test.go            package backend_test — register/login/logout
│   ├── accounts_test.go        package backend_test — account list/add/remove
│   ├── files_test.go           package backend_test — upload/download/delete
│   └── health_test.go          package backend_test — /healthz, /readyz
├── export_test.go              NEW — see below, stays in backend/ (not tests/)
└── ... (server.go, handlers*.go, etc. as in §2's tree above)
```

`backend/tests` is its own Go package (`package backend_test`) that imports
`backend` and `drivepool` and only touches their *exported* API — verified
this is a completely normal, supported layout (`go build ./...`,
`go vet ./...`, and `go test ./...` all handle a directory containing only
`_test.go` files with zero special-casing).

**The one exception**: to spin up a `Server` for these tests without a real
OAuth flow or real Drive network calls, the test needs a `Pool` pre-wired
with `testutil`'s fake `RemoteStore` — but `drivepool.Pool`'s fields
(`manifest`, `accounts`, ...) are all unexported, and `backend.Server`'s
fields are unexported too, so `backend/tests` can't construct either
directly from outside. Go's standard answer to exactly this is an
`export_test.go` file: it has the `_test.go` suffix (compiled only for
`go test`, never in production builds) but keeps `package drivepool` /
`package backend` (same-package, so it *can* see unexported fields) —
it contains no test cases itself, just a couple of exported constructors
that exist solely so other packages' tests can build fixtures:

```go
// drivepool/export_test.go — mirrors pool_test.go's existing newTestPool,
// just exposed for reuse outside this package
package drivepool

func NewPoolForTesting(m *manifest.Manifest, accounts map[string]*Account) *Pool {
	return &Pool{manifest: m, accounts: accounts}
}
```

```go
// backend/export_test.go
package backend

// NewTestServer wires a Server around a single pre-built Pool (registered
// under userID), skipping authdb/session setup that real requests need —
// backend/tests uses this plus its own session-cookie helper to drive
// requests through the real Gin router.
func NewTestServer(userID string, pool *drivepool.Pool) *Server { ... }
```

These two files are the *only* backend-related test code that can't live in
`backend/tests/` — a hard technical constraint (unexported-field access),
not a style choice — and neither contains a `func TestXxx`, so nothing
about "where the actual test cases live" is compromised.

**Scope note**: this convention applies to the new `backend/`/`frontend/`
trees only. Existing `drivepool/pool_test.go`, `storage/*_test.go`,
`p2p/*_test.go` stay exactly where they are, co-located — matching this
repo's existing, established convention, which this plan isn't touching.

---

## 3. Frontend (`frontend/`, Vue 3 + Vite)

```
frontend/                     new top-level directory, sibling to drivepool/, p2p/, backend/
├── package.json
├── vite.config.ts             dev-mode proxy: /api/* -> Go server on :8080
├── index.html
└── src/
    ├── main.ts
    ├── App.vue
    ├── router/                vue-router: /login, /register, / (dashboard)
    ├── stores/                Pinia: auth, accounts, files
    ├── api/client.ts          fetch wrapper, throws on non-2xx, attaches cookies
    └── components/
        ├── LoginView.vue / RegisterView.vue
        ├── Sidebar.vue          accounts list + usage bars + "Connect account"
        ├── TopBar.vue           search box, pool-wide totals
        ├── FileGrid.vue         Drive-like grid/list of files
        └── DropZone.vue         full-pane drag-and-drop overlay + upload
```

**Build/embed story** (corrected from the previous draft): `npm run build`
in `frontend/` emits `frontend/dist/`. Go's `//go:embed` directive can only
embed files inside the *same package directory* it's declared in — it
cannot reach into a sibling directory like `../frontend/dist`. So the build
pipeline copies the built output into `backend/dist/` (git-ignored, pure
build artifact) immediately before `go build` runs; `backend/assets.go`
then does `//go:embed dist` against that local copy. Gin serves it via
`r.StaticFS`/`r.NoRoute` with a fallback to `index.html` for `vue-router`'s
client-side routes. Result is still a single self-contained `bin/rhino`
binary in production — Node is only needed at build time, not at runtime.

**Dev workflow**: run `npm run dev` (Vite dev server, hot reload) alongside
`go run ./cmd/rhino serve`, with Vite's `server.proxy` forwarding `/api/*` to
the Go server. Two processes only during development, no copy-into-backend
step needed since Vite serves its own assets directly in dev mode.

**Makefile additions**:
```makefile
build-frontend:
	npm --prefix frontend ci
	npm --prefix frontend run build
	rm -rf backend/dist
	cp -r frontend/dist backend/dist   # go:embed can't reach a sibling dir

build-portal: build-frontend
	go build -o bin/rhino ./cmd/rhino

run-portal: build-portal
	./bin/rhino serve
```
(`cp -r` assumes a POSIX-ish shell, same as this repo's existing `Makefile`
already does via git-bash/WSL on this Windows checkout — swap for a tiny Go
helper if that assumption ever breaks, e.g. `go run ./tools/syncassets`.)

### UI layout
- **Sidebar**: every connected account + a usage bar (green <70%, amber <90%,
  red ≥90%, matching `AccountStatus.Available`/`Limit` already returned by
  `ListAccountStatus`), "+ Connect account" button at the bottom.
- **Top bar**: pool-wide totals (same numbers `rhino status` prints today),
  search box hitting `GET /api/files?prefix=`.
- **Main pane**: file grid/list (name, size, status, date); a full-pane
  "drop files here" overlay shown on `dragover`, hidden on `dragleave`/`drop`.
- **Row actions**: download, delete (delete asks purge y/n, mirroring the
  CLI's `--purge` flag).

### Tests: `frontend/tests/`

No equivalent friction here — Vitest (the natural test runner alongside
Vite, since it reuses the same config/transform pipeline) is happy to look
for tests anywhere you point it, so this is a plain configuration choice,
not a workaround:

```
frontend/
├── vite.config.ts             adds a `test: { include: ['tests/**/*.test.ts'], environment: 'jsdom' }`
│                              block so Vitest only looks in tests/, not next to components
└── tests/
    ├── setup.ts                jsdom setup / global test config
    ├── components/
    │   ├── FileGrid.test.ts
    │   ├── Sidebar.test.ts
    │   └── DropZone.test.ts     drag-drop events are simulated here (jsdom doesn't
    │                            do real browser drag-and-drop — see e2e/ below for that)
    ├── stores/
    │   └── auth.test.ts
    ├── api/
    │   └── client.test.ts
    └── e2e/                     optional follow-up: Playwright specs driving a real
                                  browser for the one thing jsdom can't — actual
                                  HTML5 drag-and-drop and the full login->upload flow
```

New dev dependencies: `vitest`, `@vue/test-utils`, `jsdom` — all dev-only,
no impact on the production `frontend/dist` bundle. `npm run test` (added
to `package.json`) runs Vitest; CI's `ci.yml` (§8.6) gets a step for it
alongside the existing `npm run build`.

---

## 4. Wiring `rhino serve`

```go
// cmd/rhino/serve.go
func newServeCmd() *cobra.Command {
	var addr, certFile, keyFile string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the web portal",
		RunE: func(cmd *cobra.Command, args []string) error {
			srv, err := backend.New(cmd.Context(), backend.Config{
				BaseDataDir:        cfgDir, // same configDir()-derived value main.go already resolves
				ClientSecretPath:   secretPath,
			})
			if err != nil {
				return err
			}
			httpSrv := &http.Server{Addr: addr, Handler: srv.Router()} // gin.Engine implements http.Handler
			if certFile != "" {
				return httpSrv.ListenAndServeTLS(certFile, keyFile)
			}
			return httpSrv.ListenAndServe() // localhost/dev only, no TLS
		},
	}
	// ...flags...
}
```

Registered alongside the existing subcommands in `main()` — `account`,
`put`, `get`, `ls`, `rm`, `status` are untouched.

---

## 5. Security (mandatory reading before this goes on a real network)

- **TLS**: either pass `--tls-cert`/`--tls-key` directly, or (recommended
  default) run `rhino serve` on localhost behind a reverse proxy (Caddy/
  nginx) that terminates TLS — keeps the Go server itself simple. Document
  both in the README once built; don't ship this reachable over a network
  without one of the two.
- **Passwords**: bcrypt, never logged, never stored in the manifest DB.
- **Sessions**: `gin-contrib/sessions` cookie store, HttpOnly + Secure +
  SameSite=Lax; userID is derived *only* from the verified session — never
  trust a client-supplied user ID/label in any route, since that's the
  whole isolation boundary between tenants.
- **Brute force**: basic per-username/IP backoff or lockout on
  `/api/auth/login` — worth having from day one now that this is
  internet-reachable, unlike the original localhost-only CLI.
- **Registration**: v1 defaults to open self-registration; flag this
  explicitly — if it should be invite-only/admin-created instead, that's a
  one-function change (`handlers_auth.go`'s register handler) but changes
  the threat model, so decide before exposing this publicly.
- **Token-at-rest risk, specific to multi-tenancy**: every user's Drive
  refresh token now lives in a file readable by one server process (instead
  of "your own token, on your own machine, readable only by you," which was
  the CLI's model). The blast radius of a server compromise is now "every
  connected Drive account of every user," not one person's. `storage.
  CopyEncrypt`/`NewEncryptionKey` (already used for file bytes) are directly
  reusable to encrypt tokens at rest under a server-held key — recommended
  before this holds real users' credentials, tracked as a hardening
  follow-up (§6 phase 6) rather than blocking the first working version.

---

## 6. Phased delivery

0. **Chunking core** (§1.2): `PutStream`/`GetStream`, `placementTracker`,
   bounded concurrent upload/download, in `drivepool` — plus the new
   `pool_test.go` cases against the existing fake `RemoteStore`. This is a
   prerequisite for phase 1, since the backend's file handlers call
   `PutStream`/`GetStream` directly rather than `Put`/`Get`. `cmd/rhino`
   needs zero changes here — `Put`/`Get`'s signatures are unchanged, so the
   CLI keeps working against the new chunked implementation for free, and
   this phase is fully verifiable via `pool_test.go` before any HTTP layer
   exists.
1. **Backend core**: `backend/authdb` (register/login/logout/session), the
   per-user `poolCache` (built on `drivepool.OpenFromDataDir`, §1.1), Gin
   JSON handlers for accounts/files wired to `PutStream`/`GetStream` — no
   UI yet, just curl/Postman-testable.
2. **Frontend shell**: Vue+Vite scaffold, login/register views, dashboard
   with account sidebar + file list wired to the real API (list, download,
   delete — no drag-drop yet).
3. **Drag-and-drop upload** + usage bars + search.
4. **"Connect Drive account" from the browser** — reuses
   `auth.RunConsentFlow` unchanged, just triggered by a button instead of
   `rhino account add`.
5. **Upload/download progress** (SSE or WebSocket) — now just an event
   emitted per finished chunk from the loops phase 0 already built (§1.2's
   "side benefit"), not a new core-package change — the hooks already exist
   once phase 0 lands.
6. **Hardening**: token-at-rest encryption, login rate-limiting, reverse
   proxy + TLS docs, idle-Pool eviction tuning.
7. **Containerize & deploy** (§8): Dockerfile, docker-compose + Caddy,
   env-based config, health checks, graceful shutdown, schema migrations,
   CI/CD. Can start in parallel with phases 2-4 once §8.2's env-var config
   and §8.5's migration wrapper are in — both are additive and don't block
   frontend work.

---

## 7. Full repo layout once this lands

Everything under `backend/` and `frontend/` is new. `p2p/`, `storage/`,
`main.go`, `server.go` stay completely untouched. `drivepool/` keeps its
public `Put`/`Get` signatures (CLI unaffected) but gains real chunking
internals (§1.2) plus the shared bootstrap functions (§1.1) — the only
package in this plan with real logic changes, not just additions. Marked
`NEW` / `MODIFIED` against what exists today:

```
rhino-framework/
├── main.go                        unchanged
├── server.go                       unchanged
├── go.mod / go.sum                 MODIFIED — gin-gonic/gin and
│                                    gin-contrib/sessions added direct;
│                                    bcrypt promoted indirect->direct;
│                                    otelhttp promoted indirect->direct (§8.4)
├── Makefile                        MODIFIED — build-frontend, build-portal,
│                                    run-portal, vet, fmt-check, docker-build targets
├── .gitignore                      MODIFIED — bin/, frontend/node_modules/,
│                                    frontend/dist/, backend/dist/, .env
├── .dockerignore                   NEW
├── Dockerfile                      NEW — multi-stage: node build -> go build -> runtime
├── docker-compose.yml              NEW — app + Caddy + a named data volume
├── Caddyfile                       NEW — reverse proxy, automatic TLS
├── .env.example                    NEW — documents RHINO_* env vars, no real secrets
├── .github/workflows/
│   ├── ci.yml                       NEW — go vet/test/build + npm build, on every PR
│   └── release.yml                  NEW — on tag: build + push image to GHCR
│
├── storage/                        unchanged
├── p2p/                             unchanged
│
├── drivepool/                       MODIFIED — see §1.2: Put/Get signatures
│   │                                  unchanged (CLI unaffected), but now wrap
│   │                                  new PutStream/GetStream that actually chunk
│   ├── pool.go                        MODIFIED — Put/Get become thin wrappers;
│   │                                  PutStream/GetStream added (§1.2)
│   ├── placement.go                   MODIFIED — pickAccount untouched, new
│   │                                  placementTracker added alongside it (§1.2)
│   ├── bootstrap.go                  NEW — ResolveDataDir, OpenFromDataDir (§1.1),
│   │                                  shared by cmd/rhino and backend/
│   ├── export_test.go                NEW — test-only seam (§2 "Tests"), no test
│   │                                  cases, just NewPoolForTesting for backend/tests
│   ├── pool_test.go                   MODIFIED — new chunking/placementTracker
│   │                                  cases, reusing the existing fake RemoteStore
│   ├── auth/                         unchanged
│   ├── gdrive/                       unchanged
│   └── manifest/
│       ├── manifest.go                unchanged — same queries, same Go API
│       ├── schema.go                  MODIFIED — see §8.5: same resulting schema,
│       │                              wrapped in a versioned migration instead of
│       │                              one static CREATE-TABLE blob
│       └── migrations/                NEW
│           ├── migrate.go               ~40-line embedded-SQL runner + schema_migrations table
│           └── 0001_init.sql            today's schema.go contents, frozen as migration 1
│
├── backend/                         NEW — entire web-portal HTTP layer (Gin)
│   ├── server.go                     gin.Engine, route table
│   ├── config.go                     reads RHINO_* env vars (§8.2)
│   ├── handlers_auth.go              register/login/logout
│   ├── handlers.go                   accounts/files, userID from session middleware
│   ├── handlers_health.go            /healthz, /readyz (§8.3)
│   ├── middleware.go                  session-verification gin.HandlerFunc
│   ├── poolcache.go                  per-user *drivepool.Pool cache (§1.1)
│   ├── assets.go                     //go:embed dist
│   ├── dist/                          git-ignored, frontend/dist copied here (§3)
│   ├── export_test.go                NEW — test-only seam (§2 "Tests"), no test
│   │                                  cases, just NewTestServer for backend/tests
│   ├── tests/                        NEW — all backend test cases live here (§2 "Tests")
│   │   ├── testutil/fakestore.go
│   │   ├── auth_test.go / accounts_test.go / files_test.go / health_test.go
│   └── authdb/
│       ├── users.go                   users(id, username, password_hash, created_at)
│       └── migrations/                 same pattern as drivepool/manifest/migrations
│
├── frontend/                         NEW — Vue 3 + Vite frontend source
│   ├── package.json / package-lock.json
│   ├── vite.config.ts                 dev proxy: /api/* -> :8080; test.include -> tests/
│   ├── index.html
│   ├── dist/                          git-ignored Vite build output (copied into backend/dist, §3)
│   ├── tests/                          NEW — all frontend test cases live here (§3 "Tests")
│   │   ├── setup.ts, components/, stores/, api/, e2e/
│   └── src/
│       ├── main.ts, App.vue
│       ├── router/, stores/, api/client.ts
│       └── components/ (LoginView, Sidebar, TopBar, FileGrid, DropZone, ...)
│
├── cmd/rhino/
│   ├── main.go                       MODIFIED — openPool()/configDir() shrink to thin
│   │                                  wrappers around drivepool.ResolveDataDir/
│   │                                  OpenFromDataDir (§1.1); registers newServeCmd()
│   └── serve.go                      NEW — `rhino serve`
│
├── bin/                              fs, rhino — see §8.6 re: no longer committing these
├── tasks/web_portal.md               this file (gitignored)
└── README.md                         MODIFIED — portal usage + deployment section
```

---

## 8. Production readiness & deployment

### 8.1 Containerization

Multi-stage `Dockerfile` so the final image contains neither Node nor the Go
toolchain — just the compiled binary:

```dockerfile
# --- stage 1: build the Vue app ---
FROM node:22-alpine AS web-build
WORKDIR /frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build                 # -> /frontend/dist

# --- stage 2: build the Go binary (embeds backend/dist via go:embed) ---
FROM golang:1.25-alpine AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-build /frontend/dist ./backend/dist
RUN CGO_ENABLED=0 go build -o /out/rhino ./cmd/rhino

# --- stage 3: minimal runtime ---
FROM gcr.io/distroless/static-debian12
COPY --from=go-build /out/rhino /usr/local/bin/rhino
EXPOSE 8080
ENTRYPOINT ["rhino", "serve"]
```

`docker-compose.yml` wires the app to a reverse proxy and a persistent
volume for all durable state:

```yaml
services:
  rhino:
    build: .
    environment:
      - RHINO_DATA_DIR=/data
      - RHINO_ADDR=:8080
      - RHINO_SESSION_SECRET_FILE=/run/secrets/session_secret
      - RHINO_CLIENT_SECRET_FILE=/run/secrets/client_secret
    volumes:
      - rhino-data:/data
    secrets: [session_secret, client_secret]

  caddy:
    image: caddy:2-alpine
    ports: ["443:443", "80:80"]
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile
      - caddy-data:/data
    depends_on: [rhino]

volumes:
  rhino-data:
  caddy-data:
secrets:
  session_secret: { file: ./secrets/session_secret.txt }
  client_secret: { file: ./secrets/client_secret.json }
```

`Caddyfile` terminates TLS automatically (Let's Encrypt) and proxies to the
app — no certificate handling needed in Go itself:

```
portal.example.com {
    reverse_proxy rhino:8080
}
```

### 8.2 Configuration via environment variables

`drivepool.ResolveDataDir` (§1.1) already takes an override string — both
`cmd/rhino/main.go` and `backend/config.go` pass `os.Getenv("RHINO_DATA_DIR")`
into it, so the container just sets an env var instead of the CLI's default
`os.UserConfigDir()` resolution:

```go
// cmd/rhino/main.go
dataDir, err := drivepool.ResolveDataDir(os.Getenv("RHINO_DATA_DIR"))
```

Same pattern for `RHINO_ADDR`, `RHINO_SESSION_SECRET`(_FILE),
`RHINO_CLIENT_SECRET`(_FILE) — `backend/config.go` reads these with a
flag-or-env precedence so the CLI's local/interactive use is untouched and
the container just sets env vars instead of passing flags.

**Secrets never go in the image or the repo**: `client_secret.json` (the
Google OAuth app credential) and the session signing key are mounted at
runtime (Docker secrets above, or a k8s `Secret` volume) — this is the same
principle already in place for the CLI (`%APPDATA%\rhino\client_secret.json`
lives outside the repo; `.gitignore` already keeps local/agent config out).

### 8.3 Health checks & graceful shutdown

```go
r.GET("/healthz", func(c *gin.Context) {
    c.Status(http.StatusOK) // process is up, nothing more
})
r.GET("/readyz", func(c *gin.Context) {
    if err := s.users.Ping(c.Request.Context()); err != nil {
        c.String(http.StatusServiceUnavailable, "not ready")
        return
    }
    c.Status(http.StatusOK)
})
```

`gin.Engine` is just an `http.Handler` — it's wrapped in a regular
`*http.Server` (§4) so graceful shutdown works exactly as it would with any
Go HTTP server. `cmd/rhino/serve.go` wires `signal.NotifyContext` so the
process drains in-flight requests and closes every cached Pool's manifest
handle on `SIGTERM`/`SIGINT`, instead of the container runtime hard-killing
it:

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()
go func() {
    <-ctx.Done()
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
    defer cancel()
    httpSrv.Shutdown(shutdownCtx) // stops accepting, drains, then closes cached Pools
}()
```

### 8.4 Logging & observability

Swap the ad hoc `fmt.Println`/`fmt.Fprintln(os.Stderr, ...)` used by the CLI
today for `log/slog` (stdlib, no new dependency) inside `backend/` — JSON
handler in production (container log aggregation reads structured fields),
text handler for local `go run`. Gin's default `gin.Logger()` middleware can
be swapped for a small `slog`-based logging middleware to keep request logs
structured too.

Worth noting since it's already sitting in `go.sum` as a transitive
dependency of the Google API client: `go.opentelemetry.io/contrib/
instrumentation/net/http/otelhttp` is *already indirectly present*. Wrapping
the `*http.Server`'s handler in `otelhttp.NewHandler(r, "rhino-portal")`
(where `r` is the `gin.Engine`) gets request tracing/metrics essentially for
free — no new module fetch, just promoting an existing indirect dependency
to direct.

### 8.5 Schema migrations

`drivepool/manifest/schema.go` currently applies one static
`CREATE TABLE IF NOT EXISTS` blob — fine for a single-evolution local tool,
not durable enough once real users' data lives in it across deploys. Add a
tiny embedded-SQL migrator (no new dependency — `database/sql` + `go:embed`
is enough for a project this size; `golang-migrate` would be the heavier
alternative if the migration set grows large):

```go
//go:embed *.sql
var migrationFS embed.FS

func Migrate(db *sql.DB) error {
    db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`)
    files, _ := migrationFS.ReadDir(".")
    sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })
    for _, f := range files {
        version := versionFromName(f.Name()) // "0001_init.sql" -> 1
        var applied bool
        db.QueryRow(`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = ?)`, version).Scan(&applied)
        if applied {
            continue
        }
        sqlBytes, _ := migrationFS.ReadFile(f.Name())
        if _, err := db.Exec(string(sqlBytes)); err != nil {
            return fmt.Errorf("migration %s: %w", f.Name(), err)
        }
        db.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, version)
    }
    return nil
}
```

`0001_init.sql` is today's `schema.go` contents verbatim, so the resulting
schema and the `Manifest`/`Pool` Go API are identical to today — this is a
mechanism change (versioned, ordered files instead of one blob), not a
behavior change, and doesn't touch anything `pool_test.go` exercises. The
new `backend/authdb` gets its own tiny migration set the same way. Once
Phase-3 chunking (`tasks/modification_plan.md`) needs schema changes, they
land as `0002_...sql` instead of editing `0001_init.sql` in place.

### 8.6 CI/CD

`.github/workflows/ci.yml` on every push/PR:
1. `go vet ./...`
2. `go test ./... -v` — already picks up `backend/tests` and `drivepool`'s
   `export_test.go` automatically, no special CI wiring needed for the new
   test layout
3. `go build ./...`
4. `npm --prefix frontend ci && npm --prefix frontend run build`
5. `npm --prefix frontend run test` — Vitest, from `frontend/tests/`

`.github/workflows/release.yml` on a version tag (`v*`):
1. Build the multi-stage Docker image
2. Push to GHCR tagged with the git tag + `latest`

**Repo hygiene note**: `bin/fs`/`bin/rhino` are currently committed
(CLAUDE.md flags this — no `.gitignore` entry exists for `bin/` today).
Once CI/Docker is the real build+release path, committed binaries are
redundant and will drift from source; recommend adding `bin/` to
`.gitignore` at that point. Not doing this now — flagging it since it's a
one-line change that pairs naturally with standing up CI, not something to
do silently as a side effect of this feature.

### 8.7 Backup

The entire durable state of a deployment is: `users.db`, every
`users/<id>/manifest.db`, every `users/<id>/accounts/*` token file, and
`client_secret.json` — all under the one volume mounted at `RHINO_DATA_DIR`.
Back up that volume. For the SQLite files specifically, prefer
`sqlite3 manifest.db ".backup /backup/manifest.db"` (or `VACUUM INTO`) over
a raw file copy, since copying a live SQLite file mid-write can grab a
torn/inconsistent snapshot.

---

## Open questions (defaults chosen above, flag if wrong)

- **Registration policy**: open self-registration (default above) vs.
  invite-only/admin-created accounts.
- **Session mechanism**: `gin-contrib/sessions` cookie store (default
  above, updated from an earlier hand-rolled-cookie draft now that Gin is
  the confirmed framework) vs. a fully custom implementation.
- **TLS**: terminate in Go itself vs. always require a reverse proxy in
  front (default above leans reverse proxy).
