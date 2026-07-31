# RIHNO Framework 🦏

Two related distributed-storage subsystems, sharing one on-disk storage/encryption layer:

1. **A peer-to-peer, content-addressable file store** (`server.go`, `p2p/`) — nodes talk to each other over raw TCP, store file content on local disk using a SHA-1 content-addressable path layout, and encrypt every file's bytes with AES-256-CTR before they're written to disk or sent across the wire. A node that doesn't have a file locally can ask its peers for it and stream it back on demand.
2. **A Google-Drive-pooled storage CLI** (`drivepool/`, `cmd/rhino`) — pools multiple free Google Drive accounts (15GB each) into one virtual storage volume. Each file is encrypted client-side, split into chunks, and each chunk uploaded (concurrently) to whichever registered account currently has the most free space at that moment — so one file's size is no longer capped by any single account's remaining quota. Tracked in a shared Postgres database via GORM (e.g. [Neon](https://neon.tech)), the same one the web portal uses — see "Google Drive pooling" below.
3. **A multi-user web portal** (`backend/`, `frontend/`, `rhino serve`) — a Google-Drive-like browser UI on top of the same pooling engine: drag-and-drop upload/download, a sidebar showing every connected account and its fill level, and its own login system so multiple people can each pool their own Drive accounts through one deployment. See "Web portal" below.

## How it works

- **Transport (`p2p/`)** — `Transport` and `Peer` are interfaces, so the networking layer isn't hard-wired to TCP. `TCPTransport`/`TCPPeer` is the only implementation today: it dials/accepts TCP connections, runs a per-connection read loop, and decodes incoming bytes into `RPC` values. A one-byte marker (`IncomingMessage` vs `IncomingStream`) tells the read loop whether the next bytes are a discrete gob-encoded message or a raw file stream that should bypass decoding and be handed off to whoever is waiting on it (via a `sync.WaitGroup` per peer).
- **Storage (`storage/store.go`)** — `Store` writes/reads/deletes file content under a root folder (default `ggnetwork`), namespaced per-node by an `id`. `CASPathTransformFunc` hashes the storage key with SHA-1 and splits the hex digest into nested 5-character directory segments (e.g. `68044/29f74/181a6/...`), so files are content-addressable and never dumped into one giant flat directory. `storage` is its own importable package (not `package main`) so other subsystems — like the Google-Drive pooling feature described below — can reuse it.
- **Encryption (`storage/crypto.go`)** — `CopyEncrypt`/`CopyDecrypt` stream-encrypt file bytes with AES-256 in CTR mode, generating a random IV per encryption and prepending it to the ciphertext so the receiving side can pull it back out. `GenerateID` mints a random per-node ID; `HashKey` (MD5) obfuscates the storage key sent over the network so peers don't see plaintext filenames.
- **Distributed file server (`server.go`)** — `FileServer` ties the above together:
  - `Store(key, reader)` writes the file locally, then broadcasts a `MessageStoreFile` to all connected peers and streams the AES-encrypted bytes to them so they hold a replica too.
  - `Get(key)` serves the file from local disk if present; otherwise it broadcasts a `MessageGetFile` request, waits briefly, and reads back the encrypted bytes from whichever peer responds, decrypting them into local storage before returning a reader.
  - `bootstrapNetwork()` dials every address in `BootstrapNodes` on startup so a new node joins the existing network.
  - Message types are registered with `encoding/gob` (`init()` in `server.go`) since `Message.Payload` is sent as an `any` and gob needs concrete types registered up front.

## Current status / known gaps

This is a from-scratch systems project, and it's mid-build:

- `main.go` currently only spins up a bare `TCPTransport` on `:3000` and prints whatever `RPC`s it receives — it does **not** wire up `FileServer`, so there's no ready-to-run CLI for a real multi-node P2P file network yet. The `OnPeer` callback in `main.go` also just calls `Peer.Close()` immediately, which is placeholder wiring rather than real peer handling.
- The compiled binary (`bin/fs`) is currently committed to git. Regenerating it via `make build` will modify a tracked file.
- No CI/linter config existed for the P2P side originally; `.github/workflows/ci.yml` now runs `go vet`/`go build`/`go test` plus the frontend build/tests on every push, but nothing enforces it locally beyond `make test`/`make vet`.
- **The web portal's upload/download progress is chunk-level, not byte-level** (a live event per finished chunk, not a smooth progress bar) — true byte-granular progress would need a callback added inside a single chunk's transfer, which isn't built.
- **No login rate-limiting** on the portal's `/api/auth/login` yet — worth adding before exposing a deployment publicly.
- **Chunk replication isn't implemented** — the `chunk_replicas` table exists in the schema, but each chunk still lives on exactly one account, so that account going down fails that file's download. Only the account-spreading part of the original chunking plan is done.

## File structure

```
main.go                 Entry point. Currently a minimal demo: starts a TCPTransport on :3000
                         and logs incoming RPCs. Does not yet construct/start a FileServer.
server.go                FileServer / FileServerOpts — the distributed file API (Store/Get),
                         peer map, message broadcast + handling, network bootstrap.
                         Imports storage/ for on-disk storage + encryption.

storage/                 Importable package — on-disk content-addressable storage + crypto helpers,
                         shared by server.go (P2P) and drivepool/ (Drive pooling).
├── store.go               Store / StoreOpts, CASPathTransformFunc (SHA-1 based path sharding),
│                          PathKey, DefaultPathTransformFunc (identity, used in tests).
├── store_test.go          Tests: CAS path transform, write/read/has/delete round trip.
├── crypto.go              GenerateID, HashKey (MD5), NewEncryptionKey, CopyEncrypt/CopyDecrypt
│                          (AES-256-CTR streaming encryption with a prepended random IV).
└── crypto_test.go         Test: encrypt -> decrypt round trip.

p2p/
├── transport.go          Peer and Transport interfaces — the transport-agnostic contract.
├── tcp_transport.go       TCPTransport / TCPPeer — TCP implementation of Transport
│                          (dial/accept loop, per-connection read loop, stream handling).
├── tcp_transport_test.go  Smoke test for TCPTransport.
├── handshake.go           Pluggable HandshakeFunc; only NOPHandshakeFunc (no-op) exists today.
├── message.go             RPC struct + IncomingMessage/IncomingStream framing byte constants.
└── encoding.go            Decoder interface, GOBDecoder and DefaultDecoder implementations.

drivepool/               Google Drive multi-account pooling — core domain logic.
├── pool.go                Pool / Account — registers accounts; Put/Get are thin wrappers (for the
│                          CLI's local-file use case) around PutStream/GetStream, which actually
│                          split a file into chunks, encrypt each in memory (no local temp file),
│                          and upload/download them concurrently across accounts.
├── placement.go           pickAccount (single-chunk, live-queried) + placementTracker (queries
│                          every account's quota once, then places many chunks from memory).
├── bootstrap.go            DefaultClientSecretPath / OpenWithUser — wires OAuth client config +
│                          an already-open manifest.Manifest into a Pool for a given userID.
├── testing.go              NewPoolForTesting — regular exported helper (not test-gated: a
│                          separate package's tests can't see export_test.go-style seams) used by
│                          this package's own tests and by backend/tests.
├── pool_test.go / account_test.go / chunking_test.go / fileinfo_test.go   Tests against a fake
│                          in-memory RemoteStore (no real Drive calls) *and* a real Postgres
│                          transaction (db/dbtest, rolled back automatically) — placement,
│                          Put/Get round-trip, chunk spreading across accounts, partial-failure
│                          cleanup, tampered-chunk detection, cross-tenant isolation.
├── auth/
│   └── consent.go           RunConsentFlow — OAuth2 loopback-redirect consent flow.
├── gdrive/
│   └── client.go             RemoteStore interface + Client — Drive API v3 wrapper
│                              (EnsureFolder/Upload/Download/Delete/Quota).
└── manifest/
    ├── models.go              GORM model structs: Account, VirtualFile, Chunk, ChunkReplica.
    ├── manifest.go            Manifest — GORM queries, all scoped by userID (see scoped()); also
    │                          handles OAuth token encryption at rest.
    └── manifest_test.go       Tenant isolation, cascade/set-null FK behavior, token round-trip.

db/                      Shared Postgres connection/migration package — used by both cmd/rhino
                         and backend/, neither opens its own connection.
├── db.go                  Open (pooled *gorm.DB, tuned for Neon), IsUniqueViolation,
│                          DatabaseURLFromEnv/TokenEncryptionKeyFromEnv.
├── migrate.go             Migrate — applies db/migrations/*.sql via golang-migrate, idempotent,
│                          run at startup by every entrypoint.
├── migrations/             Numbered plain-SQL migration files (not GORM AutoMigrate).
└── dbtest/                 Test-only: OpenTx wraps each test in a rolled-back transaction.

backend/                 Web portal's Gin-based HTTP API (see "Web portal" below).
├── server.go              Server/gin.Engine, route table, graceful-shutdown-friendly Router().
├── config.go              RHINO_*/DATABASE_URL env var resolution (Postgres connection, token
│                          encryption key, session secret, TLS).
├── poolcache.go           Per-user *drivepool.Pool cache — lazily opened, idle-evicted.
├── middleware.go          Session-verification middleware; userID never trusted from the client.
├── handlers_auth.go        register/login/logout/me.
├── handlers.go             accounts/files CRUD — upload/download stream straight into
│                          PutStream/GetStream, no server-side temp file.
├── handlers_health.go      /healthz, /readyz.
├── assets.go               Embeds the built frontend (backend/dist/, see below) and serves it
│                          with an SPA fallback for vue-router's client-side routes.
├── testing.go              NewTestServer — regular exported helper (same export_test.go
│                          limitation as drivepool/testing.go) used by backend/tests.
├── dist/                   go:embed target; only index.html is committed as a placeholder —
│                          `make build-frontend` overwrites this with the real Vite build output.
├── authdb/                 users(id, username, password_hash, created_at) — GORM, same shared
│                          Postgres database as drivepool/manifest (see db/), distinct table.
└── tests/                  All backend test cases, as an external package hitting the real
                            Gin router via httptest — see "Testing" below.

frontend/                Vue 3 + Vite SPA — Drive-like dashboard, drag-and-drop upload.
├── src/
│   ├── views/               LoginView, RegisterView, DashboardView (route-level pages).
│   ├── layouts/              PortalLayout (sidebar + top bar shell).
│   ├── components/           Sidebar (accounts + usage bars), TopBar (search/totals/logout),
│   │                          FileGrid (file table + in-progress uploads), DropZone (full-page
│   │                          drag-and-drop overlay).
│   ├── stores/                Pinia: auth, accounts, files.
│   ├── api/client.ts           fetch/XHR wrapper for the backend's JSON API.
│   ├── composables/useBytes.ts formatBytes/usageLevel, mirroring the CLI's humanBytes.
│   └── router/                vue-router with a session-aware navigation guard.
└── tests/                  Vitest test cases (components/stores/api), separate from src/ —
                            see "Testing" below.

cmd/rhino/
├── main.go                cobra CLI: account add/list/remove, put, get, ls, rm, status. Every
│                          command requires --user/RHINO_USER — no default identity.
└── serve.go                `rhino serve` — runs the web portal; graceful shutdown on
                            SIGINT/SIGTERM via signal.NotifyContext + http.Server.Shutdown.

bin/fs                  Build output of `make build` (currently checked into git).
Makefile                 build / run / test / vet / build-rhino / run-rhino /
                        build-frontend / build-portal / run-portal targets.
go.mod / go.sum          Module github.com/Souvik-223/rhino-framework, Go 1.25.
Dockerfile / docker-compose.yml / Caddyfile   Container build + reverse-proxy deployment
                        for the portal — see "Web portal" → "Deploying" below.
```

## Requirements

- Go 1.25 or newer (`go.mod` currently declares `go 1.25.0`, bumped automatically when the Google API client libraries were added).
- The P2P side needs no database, environment variables, or external services.
- The Google Drive pool needs **a Postgres database** (`DATABASE_URL` — [Neon](https://neon.tech)'s free tier works well, or any Postgres 14+) and a one-time Google Cloud OAuth setup (free) — see "Google Drive pooling" below. Every account/file/chunk row lives in that database now; the only thing still resolved to a local file by default is `client_secret.json` (the OAuth app credential), under your OS config dir (e.g. `%APPDATA%\rhino` on Windows).
- The web portal additionally needs **Node.js 22+** to build the frontend (`make build-frontend`) — only at build time. The resulting `bin/rhino` binary embeds the built assets and needs no Node/npm at runtime.

## Getting started

```bash
git clone <repo-url>
cd rhino-framework
go mod download   # fetch all dependencies
make build         # go build -o bin/fs
make run           # builds, then runs ./bin/fs — listens on TCP :3000
```

`make run` currently just starts the bare TCP listener from `main.go` and prints any `RPC` it receives — connect to it with `nc localhost 3000` and send bytes to see them logged.

> **No `make` on your machine?** The `Makefile` is just a thin wrapper — nothing here depends on it. Run the underlying Go commands directly instead:
> ```bash
> go build -o bin/fs .              # instead of make build
> go build -o bin/rhino ./cmd/rhino # instead of make build-rhino
> go test ./... -v                  # instead of make test
> ```
> **Windows note:** if you're in PowerShell (not Git Bash/WSL), build with an explicit `.exe` suffix — `go build -o bin/rhino.exe ./cmd/rhino` — and run it as `.\bin\rhino.exe`. PowerShell refuses to execute a binary that lacks the `.exe` extension even though it's a perfectly valid Windows executable; Git Bash doesn't have this restriction, so `./bin/rhino` (no extension) works fine there.

## Running tests

```bash
export DATABASE_URL="postgres://postgres:postgres@localhost:5432/rhino_dev?sslmode=disable"  # any real Postgres works; see gudeMD/testing.md §1 for a Podman/Docker one-liner
make test          # go test ./... -v
make vet           # go vet ./...
```

`drivepool`/`drivepool/manifest`/`backend/tests` all need `DATABASE_URL` pointing at a real Postgres — there's no local zero-config fallback (each test runs inside its own transaction, rolled back automatically, via `db/dbtest`). Without it, those packages' tests `t.Skip` rather than fail. `storage`/`p2p` need nothing.

Covers: CAS path-sharding + store read/write/delete (`storage/store_test.go`), AES encrypt/decrypt round trip (`storage/crypto_test.go`), a TCP transport smoke test (`p2p/tcp_transport_test.go`), and the full `drivepool` `Put`/`Get`/`PutStream`/`GetStream` pipeline — encrypt → chunk → place → upload → download → decrypt → verify, plus chunk placement across accounts, partial-upload-failure cleanup, tampered-chunk detection, and multi-tenant isolation (`drivepool/pool_test.go` and friends, `drivepool/manifest/manifest_test.go`). All of this runs against a fake in-memory Drive account, so no real Google credentials are needed.

`backend/tests` covers the portal's HTTP API the same way — register/login/logout/session enforcement, account listing, and a full upload → list → download → delete round trip — driven via `httptest` against the real Gin router, with a `Pool` wired to the same kind of fake `RemoteStore`. `drivepool/testing.go` and `backend/testing.go` exist specifically to let `backend/tests` (a separate package) build these fixtures, since `Pool`'s and `Server`'s internals are otherwise unexported.

The frontend has its own suite, kept in `frontend/tests/` rather than next to components:

```bash
cd frontend
npm run test        # vitest run
```

Covers `formatBytes`/`usageLevel`, the auth store's login/logout/session-check behavior, the API client's error handling, `FileGrid`'s empty/populated/uploading states, and `DropZone`'s drag-enter/drop handling — all with the network mocked, no backend required.

## Running multiple nodes (manual wiring)

Because `main.go` doesn't wire up `FileServer` yet, actually exercising file replication across peers means constructing a couple of `FileServer`s yourself — for example in a scratch `main` or a test:

```go
import "github.com/Souvik-223/rhino-framework/storage"

func makeServer(listenAddr string, nodes ...string) *FileServer {
	tcpOpts := p2p.TCPTransportOpts{
		ListenAddr:    listenAddr,
		HandshakeFunc: p2p.NOPHandshakeFunc,
		Decoder:       p2p.DefaultDecoder{},
	}
	tr := p2p.NewTCPTransport(tcpOpts)

	s := NewFileServer(FileServerOpts{
		EncKey:            storage.NewEncryptionKey(),
		StorageRoot:       listenAddr[1:] + "_network",
		PathTransformFunc: storage.CASPathTransformFunc,
		Transport:         tr,
		BootstrapNodes:    nodes,
	})
	tr.OnPeer = s.OnPeer // wire the transport's peer callback to the server
	return s
}

func main() {
	s1 := makeServer(":3000")
	s2 := makeServer(":4000")
	s3 := makeServer(":5000", ":3000", ":4000") // bootstraps against s1 and s2

	go s1.Start()
	time.Sleep(time.Millisecond * 500)
	go s2.Start()
	time.Sleep(time.Millisecond * 500)
	go s3.Start()
	time.Sleep(time.Millisecond * 500)

	data := bytes.NewReader([]byte("some file content"))
	s3.Store("myfile", data)          // stored locally on s3, replicated to s1 + s2

	r, _ := s3.Get("myfile")          // served straight from s3's local disk
	_, _ = io.Copy(io.Discard, r)
}
```

The key wiring detail is `tr.OnPeer = s.OnPeer` — without it, new connections are never added to `FileServer.peers`, so `Store`/`Get` have no one to broadcast to.

## Google Drive pooling (`rhino` CLI)

`drivepool` treats N registered Google Drive accounts as one pool: `rhino put` encrypts a file with AES-256-CTR (via the same `storage.CopyEncrypt` used by the P2P side), splits it into fixed-size chunks, and uploads each chunk — concurrently, entirely from memory, no local temp file — to whichever registered account currently has the most free space at that moment. `rhino get` reverses this: downloads and decrypts each chunk concurrently (verifying it against its own recorded hash), reassembling them in the correct order, then verifies the whole plaintext against a SHA-256 content hash recorded at upload time. Everything is tracked in a shared Postgres database (`accounts` / `virtual_files` / `chunks` tables, via GORM — see `db/`) so `rhino ls`/`status` can report on the pool without touching the network, and so the same data is visible whether you access it from the CLI or the web portal.

**Not yet implemented:** chunk replication (storing a chunk on more than one account for redundancy — the `chunk_replicas` table exists in the schema but isn't populated yet), dedup, and versioning.

### One-time setup

1. **A Postgres database.** [Neon](https://neon.tech) has a free tier that works well; any Postgres 14+ works. Set `DATABASE_URL` to its connection string (`.env.example` has the exact format). The schema is created automatically on first run — no manual migration step.
2. **A 32-byte token encryption key** — `openssl rand -hex 32`, set as `RHINO_TOKEN_ENCRYPTION_KEY`. This encrypts every connected account's OAuth token at rest; losing/rotating it means reconnecting every account from scratch.
3. **Google Cloud OAuth setup (free)**:
   - Go to [console.cloud.google.com](https://console.cloud.google.com), create a project, and enable the **Google Drive API**.
   - **APIs & Services → OAuth consent screen**: User type **External**, add scope `https://www.googleapis.com/auth/drive.file`, then **publish the app to "In production"**. This one click-through matters: apps left in "Testing" status get refresh tokens that expire after 7 days, forcing weekly re-auth per account. `drive.file` is a non-sensitive scope, so publishing doesn't require Google's full app-verification review — you'll just see (and can dismiss) an "unverified app" warning once per account during consent, which is expected for a personal, non-public tool.
   - **APIs & Services → Credentials → Create Credentials → OAuth client ID → Desktop app**. Download the JSON.
   - Save that file as `<config dir>/rhino/client_secret.json` — on Windows, `%APPDATA%\rhino\client_secret.json` (or pass `--credentials <path>` to every `rhino` command instead).

### Usage

Every command needs `--user <name>`/`RHINO_USER` to identify whose data it's operating on — there's no default identity. A fresh name auto-provisions a CLI-only identity; pointing it at an existing web-portal username operates on that person's real data.

```bash
make build-rhino          # or: go build -o bin/rhino ./cmd/rhino (bin/rhino.exe on Windows PowerShell)
export RHINO_USER=yourname   # or pass --user yourname to every command below

bin/rhino account add --label home-gmail
# prints a URL to open in a browser; after you approve access it's captured
# automatically via a local loopback redirect (no copy/paste of a code)

bin/rhino account add --label work-gmail
bin/rhino account list
#   home-gmail           14.8 GiB free of 15.0 GiB
#   work-gmail            9.2 GiB free of 15.0 GiB

bin/rhino put ~/photos/trip.jpg --as photos/trip.jpg
bin/rhino ls
#   photos/trip.jpg                              4.2 MiB  complete

bin/rhino get photos/trip.jpg ~/restored.jpg
bin/rhino status
#   2/2 accounts healthy | 30.0 GiB total | 24.0 GiB free | 1 files

bin/rhino rm photos/trip.jpg --purge   # also deletes the remote copy
```

If `account add` fails immediately with an error about a missing `client_secret.json`, that just means step 3/4 above hasn't been done yet — every other command (and all the automated tests) work with zero Google setup.

`rhino account remove <label>` deregisters an account locally (deletes its manifest row) without touching its remote files. If a token is revoked or expires, affected commands report that account as unavailable rather than failing the whole operation — other healthy accounts keep working.

## Web portal (`rhino serve`)

A browser-based, Google-Drive-like UI on top of the same pooling engine — drag-and-drop upload, per-file download/delete, and a sidebar showing every connected account and its fill level. Unlike the CLI (one local user, one set of accounts), the portal is **multi-user**: anyone who registers gets their own login and connects their own Drive accounts, isolated from every other user on the same deployment. The full design rationale lives in `plans/web_portal.md`.

### Running it locally

```bash
make build-frontend   # npm ci + npm run build in frontend/, then copies frontend/dist -> backend/dist
make build-portal      # build-frontend, then go build -o bin/rhino ./cmd/rhino
make run-portal         # build-portal, then ./bin/rhino serve

# or, for frontend development with hot reload (two processes):
go run ./cmd/rhino serve            # backend on :8080
cd frontend && npm run dev           # frontend dev server, proxies /api/* to :8080
```

Uses the same Postgres database and `client_secret.json` as the CLI (see above) — the OAuth app credential is deployment-wide, shared by every portal user, not something each user configures themselves. Without a `client_secret.json` in place, registration/login/health checks all work, but connecting a Drive account or listing/uploading files will fail until it's there.

Key environment variables (see `.env.example` for the full list):

| Variable | Purpose |
| --- | --- |
| `DATABASE_URL` / `DATABASE_URL_FILE` | Postgres connection string — every user's accounts/files/chunks and the portal's login accounts all live here. Required. |
| `RHINO_TOKEN_ENCRYPTION_KEY` / `RHINO_TOKEN_ENCRYPTION_KEY_FILE` | 32 bytes, hex-encoded — encrypts every stored OAuth token at rest. Required. |
| `RHINO_ADDR` | Address to listen on (default `:8080`); `--addr` overrides. |
| `RHINO_SESSION_SECRET` / `RHINO_SESSION_SECRET_FILE` | Signs session cookies. Without one, a random key is generated at startup and every session is invalidated on restart — **set this in production**. |
| `RHINO_CLIENT_SECRET` | Overrides where `client_secret.json` is read from. |

### Deploying

```bash
cp .env.example .env                          # fill in real values
mkdir -p secrets
echo -n "$(openssl rand -base64 32)" > secrets/session_secret.txt
echo -n "postgres://user:pass@your-neon-host/db?sslmode=require" > secrets/database_url.txt
echo -n "$(openssl rand -hex 32)" > secrets/token_encryption_key.txt
cp /path/to/your/client_secret.json secrets/client_secret.json

docker compose up -d --build
```

`docker-compose.yml` runs the portal behind a `Caddy` reverse proxy that terminates TLS automatically (edit the placeholder domain in `Caddyfile` first). All durable state — every user's accounts/files/chunks, and the portal's login accounts — lives in the external Postgres database (`DATABASE_URL`), not on a local volume; there's nothing to back up on the host itself beyond `client_secret.json`. `/healthz` (liveness) and `/readyz` (readiness, checks the database connection) are available for a load balancer or orchestrator.

### Testing without real Google credentials

Every automated test — CLI (`drivepool/pool_test.go`, `drivepool/chunking_test.go`) and portal (`backend/tests/`) alike — runs against a fake in-memory Drive account, never the real API. Manually exercising the portal end-to-end (register → login → connect a Drive account → drag-drop a file) does need a real `client_secret.json`, same as the CLI.
