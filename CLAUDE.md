# CLAUDE.md

> Auto-loaded by Claude Code on every session in this repo.

## What this project is

**RIHNO Framework** — two Go subsystems sharing one storage/encryption layer:

1. A peer-to-peer, content-addressable distributed file store (`server.go`, `p2p/`). Nodes connect over raw TCP, store file content on disk using a SHA-1 content-addressable path layout, and AES-256-CTR-encrypt file bytes before they hit disk or the network. A node without a file locally can request it from peers and stream it back. No database, no HTTP/REST API, no web frontend — produces a single binary (`bin/fs`).
2. A Google-Drive-pooled storage CLI + web portal (`drivepool/`, `cmd/rhino`, `backend/`, `frontend/`). Pools N free Google Drive accounts (15GB each) into one virtual volume: `rhino put` encrypts a file and uploads it to whichever registered account currently has the most free space; `rhino get` reverses that. Both the CLI and the multi-tenant web portal (`rhino serve`) share one Postgres database (`DATABASE_URL`), queried via GORM — every account/file/chunk row is scoped by a `user_id` column (see `gudeMD/er_diagram.md`). Produces a second binary (`bin/rhino`).

See [README.md](README.md) for a full walkthrough of how the pieces fit together, the file structure, and how to run it.

## Stack & conventions

- **Language/module**: Go, module `github.com/Souvik-223/rhino-framework` (`go.mod` declares `go 1.25.0`, bumped automatically when the Google API client libraries were added — was `1.24.1`). `main.go`/`server.go` (repo root) are `package main`; `p2p/`, `storage/`, `drivepool/` (+ its subpackages) are separate importable packages; `cmd/rhino/` is a second `package main` producing the `rhino` binary.
- **Constructor pattern**: every major type follows `type XOpts struct {...}` + `func NewX(opts XOpts) *X` with the opts struct embedded directly into the returned struct (e.g. `type Store struct { StoreOpts }`, `type TCPTransport struct { TCPTransportOpts; ... }`, `type FileServer struct { FileServerOpts; ... }`). `drivepool.Pool`/`drivepool.Account` deviate slightly (constructed via `Open(ctx, ...)`/`AddAccount(ctx, ...)` rather than an Opts struct, since they need context + I/O to build) — follow whichever fits: Opts-struct for pure value construction, an `Open`/context-taking constructor when the type must do I/O (network calls, file/DB opens) to come into existence.
- **Interfaces over concretions**: `p2p.Transport`/`p2p.Peer` (`p2p/transport.go`) and `gdrive.RemoteStore` (`drivepool/gdrive/client.go`) are the same pattern applied twice — a small interface in its own package, one real implementation (`TCPTransport`, `gdrive.Client`), so core logic (`server.go`, `drivepool.Pool`) can be unit-tested against a fake with no real network/API calls (see `drivepool/pool_test.go`'s `fakeStore`). A new transport or a mock `RemoteStore` should satisfy the relevant interface and live in `p2p/`/`drivepool/gdrive`, not be special-cased in `server.go`/`pool.go`.
- **Serialization**: `encoding/gob` for `Message`/`RPC` payloads. Since `Message.Payload` is typed `any`, every concrete payload type must be registered with `gob.Register(...)` in the `init()` in `server.go` — remember to add new message types there.
- **Streaming vs. discrete messages**: raw file bytes are never gob-encoded. A one-byte marker (`p2p.IncomingMessage` / `p2p.IncomingStream` in `p2p/message.go`) tells the TCP read loop whether to decode the next bytes as an `RPC` or treat them as a stream to hand off directly (see `TCPTransport.handleConn` and the `sync.WaitGroup`-based `peer.CloseStream()`/`wg.Wait()` handshake).
- **Content addressing**: `storage.CASPathTransformFunc` (`storage/store.go`) — SHA-1 hash of the key, hex-encoded, split into 5-character directory segments. Don't bypass this with flat filenames when adding storage paths; use `PathTransformFunc` consistently. `drivepool` uses a SHA-256 content hash (not SHA-1) to name each file's remote Drive folder, since that hash also doubles as the integrity-check/future-dedup key — a stronger hash is warranted there.
- **Encryption**: `storage.CopyEncrypt`/`storage.CopyDecrypt` (`storage/crypto.go`) — AES-256-CTR, random IV per call, IV prepended to ciphertext. `storage.HashKey` (MD5) is only for obfuscating storage keys on the wire, not a security boundary — don't repurpose it for anything security-sensitive. Both `server.go` (P2P) and `drivepool/pool.go` (Drive pooling) share this same pipeline via the `storage` package rather than each having their own copy.
- **Compression**: `storage.CompressBytes`/`storage.DecompressBytes` (`storage/compress.go`) — raw DEFLATE (`compress/flate`, stdlib, no new dependency). Only `drivepool.Pool.uploadChunk` calls this today (not the P2P side): each chunk's plaintext is compressed *before* `CopyEncrypt`, and the compressed form is kept only if it's actually smaller (`chunks.compression_algo`/`compressed_size` record the per-chunk decision — `"none"` or `"flate"`). `downloadChunk` reverses it *after* `CopyDecrypt`, before the existing `PlaintextSHA256` check, which still verifies against the original uncompressed bytes. See `gudeMD/compression.md` for the full design and `gudeMD/er_diagram.md` for the schema.
- **Concurrency**: peer map guarded by `sync.Mutex` (`FileServer.peerLock`); per-peer stream synchronization via `sync.WaitGroup`; RPCs delivered over a buffered channel (`Transport.Consume() <-chan RPC`). Keep new concurrent state behind an explicit mutex or channel rather than ad hoc synchronization. `drivepool.Manifest` wraps a shared `*gorm.DB` (see `db.Open`) — Postgres handles concurrent access natively, no `SetMaxOpenConns(1)`-style single-writer restriction like the old SQLite backend needed.
- **Database**: one shared Postgres database (`DATABASE_URL`, e.g. Neon) for both the CLI and the web portal — no ORM-free/SQLite era left. Queries go through [GORM](https://gorm.io); schema changes are plain SQL files under `db/migrations/`, applied via [golang-migrate](https://github.com/golang-migrate/migrate) (`db.Migrate`, run at startup by both `rhino serve` and every CLI command) — **not** GORM's `AutoMigrate`, since the tenant-isolation constraints need to be reviewable in a SQL diff, not re-inferred from struct tags. Every per-user table (`accounts`, `virtual_files`, `chunks`, `chunk_replicas`) carries a `user_id` column; `drivepool/manifest/manifest.go`'s private `scoped(ctx, userID)` helper is the only sanctioned way any query is built — it panics on an empty `userID` rather than silently matching zero rows. See `gudeMD/er_diagram.md` for the full schema and rationale.
- **Resource cleanup**: every `*os.File` opened for writing must be `defer f.Close()`'d — `storage.Store`'s `writeStream`/`WriteDecrypt` previously leaked write handles (harmless on Linux/Mac, but broke `Store.Delete`/`Clear` on Windows since it can't remove a file with an open handle); this has been fixed, so don't reintroduce the pattern.
- **Graceful degradation over hard failure**: a `drivepool.Account` whose token fails to load/refresh is kept in `Pool.accounts` with `initErr` set rather than aborting `Open`/`AddAccount` — placement (`pickAccount`) and `ListAccountStatus` skip/report it instead of crashing the whole operation. Follow this same pattern for new failure modes in `drivepool` (never let one bad account take down an operation involving healthy ones).

## Directory map

```
main.go                 Entry point. Currently a minimal demo: starts a TCPTransport on :3000
                        and logs incoming RPCs. Does NOT construct/start a FileServer yet —
                        its OnPeer callback just closes the peer immediately (placeholder).
server.go               FileServer / FileServerOpts — distributed Store()/Get() API, peer map,
                        message broadcast/handling (MessageStoreFile/MessageGetFile), bootstrap.
                        Imports storage/ for on-disk storage + encryption.

storage/                Importable package — on-disk content-addressable storage + crypto helpers.
├── store.go              Store / StoreOpts, CASPathTransformFunc (SHA-1 path sharding), PathKey.
├── store_test.go         Tests: CAS path transform, write/read/has/delete round trip.
├── crypto.go             GenerateID, HashKey (MD5), NewEncryptionKey, CopyEncrypt/CopyDecrypt (AES-256-CTR).
├── crypto_test.go        Test: encrypt -> decrypt round trip.
├── compress.go           CompressBytes/DecompressBytes (raw DEFLATE) + CompressionNone/CompressionFlate
│                         constants. Only drivepool calls this today; lives here for the same reason
│                         crypto.go does — shared package for both subsystems.
└── compress_test.go      Test: compress -> decompress round trip (compressible/incompressible/empty).
                        Shared by both server.go (P2P) and drivepool/ (Drive pooling, in progress).

p2p/
├── transport.go          Peer / Transport interfaces — the transport-agnostic contract.
├── tcp_transport.go       TCPTransport / TCPPeer — TCP implementation (dial/accept, read loop).
├── tcp_transport_test.go  Smoke test for TCPTransport.
├── handshake.go           Pluggable HandshakeFunc (only NOPHandshakeFunc exists today).
├── message.go             RPC struct + IncomingMessage/IncomingStream framing constants.
└── encoding.go            Decoder interface, GOBDecoder / DefaultDecoder.

drivepool/              Google Drive multi-account pooling — core domain logic.
├── pool.go               Pool / Account — AddAccount/ListAccountStatus/RemoveAccount,
│                         Put/PutStream (chunked encrypt+upload, spread across accounts) /
│                         Get/GetStream (download+decrypt+verify), List, Remove. Pool is scoped
│                         to one userID for its whole lifetime, set once at construction (Open) —
│                         every internal manifest call passes it automatically.
├── placement.go          pickAccount / placementTracker — most-free-space-first placement,
│                         live-queried per file (single chunk) or once then tracked in-memory
│                         (multi-chunk, to avoid a placement race across concurrent uploads).
├── bootstrap.go          DefaultClientSecretPath, OpenWithUser — wires OAuth client config +
│                         an already-open manifest.Manifest into a Pool for a given userID.
├── pool_test.go, account_test.go, chunking_test.go, fileinfo_test.go, compression_test.go
│                         Tests against a fake in-memory RemoteStore (no real Drive/network
│                         calls) and a real Postgres transaction (db/dbtest) — no SQLite fallback.
├── auth/
│   └── consent.go          RunConsentFlow — OAuth2 loopback-redirect consent flow (OOB is deprecated).
├── gdrive/
│   └── client.go            RemoteStore interface + Client — Drive API v3 wrapper.
└── manifest/
    ├── models.go             GORM model structs: Account, VirtualFile, Chunk, ChunkReplica.
    ├── manifest.go           Manifest — GORM queries, all scoped by userID (see scoped()); also
    │                         handles OAuth token encryption at rest (RHINO_TOKEN_ENCRYPTION_KEY).
    └── manifest_test.go      Tenant isolation, cascade/set-null FK behavior, token round-trip.

db/                     Shared Postgres connection/migration package — used by both cmd/rhino
                        and backend/, neither opens its own connection.
├── db.go                  Open (pooled *gorm.DB, tuned for Neon), IsUniqueViolation,
│                         DatabaseURLFromEnv/TokenEncryptionKeyFromEnv (both support a
│                         Docker-secrets-style _FILE variant).
├── migrate.go             Migrate — applies db/migrations/*.sql via golang-migrate, idempotent,
│                         embedded into the binary, run at startup by every entrypoint.
├── migrations/             Numbered plain-SQL migration files — not GORM AutoMigrate; see
│                         gudeMD/er_diagram.md for why.
└── dbtest/                 Test-only: OpenTx wraps each test in a rolled-back transaction.

cmd/rhino/
├── main.go               cobra CLI: account add/list/remove, put, get, ls, rm, status.
│                         Every command requires --user/RHINO_USER (authdb.GetOrCreateCLIUser) —
│                         no default identity, no local-file fallback for its data anymore.
└── serve.go               `rhino serve` — runs the multi-tenant web portal (backend/).
                          Builds bin/rhino, separate from the P2P side's bin/fs.

backend/                Web portal: Gin HTTP API + session auth + embedded frontend build.
├── server.go / config.go / handlers*.go / middleware.go / poolcache.go / assets.go
└── authdb/                users(id, username, password_hash, created_at) — GORM, same shared
                          Postgres database as drivepool/manifest (see db/).

frontend/               Vue 3 + Vite SPA — see gudeMD/web_portal.md §3.

bin/fs                  Build output of `make build` — currently checked into git (no .gitignore).
Makefile                build / run / test / build-rhino / run-rhino / build-portal / run-portal targets.
go.mod / go.sum          Module + dependencies. GORM + Postgres driver + golang-migrate replaced
                        modernc.org/sqlite entirely (fully removed, not just unused).
```

## Commands

```bash
make build         # go build -o bin/fs
make run           # build, then run ./bin/fs (listens on TCP :3000)
make build-rhino   # go build -o bin/rhino ./cmd/rhino
make run-rhino     # build, then run ./bin/rhino
make build-portal  # build the frontend, copy into backend/dist, then go build -o bin/rhino
make run-portal    # build-portal, then run ./bin/rhino serve
make test          # go test ./... -v — needs DATABASE_URL set to a real Postgres, see below
```

There's no linter configured — at minimum run `go vet ./...` and `gofmt -l .` after non-trivial changes (note: `gofmt -l .` will flag most pre-existing files as needing formatting purely due to CRLF line endings on this Windows checkout, not real style issues — check `gofmt -d <file>` before "fixing" one of those). CI (`.github/workflows/ci.yml`) runs a `postgres:16-alpine` service container for the `go` job; locally, `go test ./...` needs `DATABASE_URL` pointing at a real Postgres or every `drivepool`/`backend` test `t.Skip`s (Podman/Docker works fine — see `gudeMD/testing.md` §1 for the exact commands). `RHINO_TOKEN_ENCRYPTION_KEY` (32 bytes hex) is also required by any code path that opens a `manifest.Manifest` — CLI and backend alike. `client_secret.json` (the Google OAuth app credential) is the one piece of state still resolved to a local file by default (`%APPDATA%\rhino\client_secret.json` on Windows) — everything else (accounts, files, chunks, users) lives in Postgres now. A `.gitignore` exists and excludes `CLAUDE.md`, `AGENTS.md`, `.claude/`, `.agent/`, and `/tasks` — these are personal/local files, not part of the shared repo.

## Working guidelines

- This is a systems/networking project, not a web app — match idiomatic Go and the patterns already in the codebase (options-struct constructors, interface-based transport, explicit mutexes/channels for concurrency), not JS/TS or framework conventions.
- New transport implementations belong in `p2p/` and must satisfy `p2p.Transport`/`p2p.Peer`; don't add transport-specific branching into `server.go`.
- New gob-serialized message payload types must be registered in the `init()` in `server.go` or decoding will fail at runtime with no compile-time warning.
- `main.go` is a bare transport smoke-test today, not the wired-up file server — if you extend it into a real CLI, wire an actual `FileServer` (per the example in [README.md](README.md#running-multiple-nodes-manual-wiring)) rather than hand-rolling networking logic directly in `main.go`.
- **`drivepool` splits large files across multiple accounts** (`Pool.PutStream`, `drivepool/placement.go`'s `placementTracker`) — a file bigger than one `ChunkSize` gets multiple `chunks` rows spread across whichever accounts have room, uploaded/downloaded concurrently. What's still schema-only/unimplemented is **replication** — `chunk_replicas` and `virtual_files.replicas` exist in the schema but nothing writes to them; each chunk still has exactly one copy, so one account going down still fails that chunk's file. Tracked as Phase 3 in `tasks/modification_plan.md` — don't assume replication exists when reasoning about durability, or when asked to add features that depend on it.
- Test `drivepool` core logic (placement, Put/Get, tenant isolation) against the fake `gdrive.RemoteStore` in `drivepool/pool_test.go` et al., not real Drive calls — there are no live Google credentials in this environment, and the fake already exercises the full encrypt→stage→upload→download→decrypt→verify path. These tests do hit a real Postgres (via `db/dbtest`'s transaction-per-test helper, rolled back automatically) — set `DATABASE_URL` before running them, see the Commands section above.
- The OAuth consent screen must be published to **"In production"**, not left in "Testing" — Testing-status refresh tokens expire after 7 days (verified against current Google docs), which `drivepool` does not currently work around with a re-auth prompt.
- Run `make test` (`go test ./... -v`) after non-trivial changes, plus `go vet ./...`. `go build ./...` covers `cmd/rhino` too.
- Never commit unless explicitly asked.

## Local AI tooling (`.claude/`, `.agent/`)

Both directories are personal, machine-local agent/skill/workflow scaffolding carried over from an unrelated (Next.js/frontend-oriented) project template — none of the frontend-, mobile-, or database-specific skills in there (`nextjs-best-practices`, `react-patterns`, `tailwind-patterns`, `frontend-design`, etc.) apply to this Go codebase. If you want deeper guidance for this stack, the generically-useful ones are:

- `.claude/skills/systematic-debugging/`
- `.claude/skills/testing-patterns/`
- `.claude/skills/clean-code/`
- `.claude/skills/bash-linux/` or `.claude/skills/powershell-windows/` (depending on shell)

There is no Go-specific skill in this scaffolding — apply standard Go idioms and the conventions already established in this codebase instead. Ignore the rest of the scaffolding (security/mobile/backend-web/MCP-registry content) as it doesn't apply here.
