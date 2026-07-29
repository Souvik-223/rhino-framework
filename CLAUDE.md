# CLAUDE.md

> Auto-loaded by Claude Code on every session in this repo.

## What this project is

**RIHNO Framework** — two Go subsystems sharing one storage/encryption layer:

1. A peer-to-peer, content-addressable distributed file store (`server.go`, `p2p/`). Nodes connect over raw TCP, store file content on disk using a SHA-1 content-addressable path layout, and AES-256-CTR-encrypt file bytes before they hit disk or the network. A node without a file locally can request it from peers and stream it back. No database, no HTTP/REST API, no web frontend — produces a single binary (`bin/fs`).
2. A Google-Drive-pooled storage CLI (`drivepool/`, `cmd/rhino`). Pools N free Google Drive accounts (15GB each) into one virtual volume: `rhino put` encrypts a file and uploads it to whichever registered account currently has the most free space; `rhino get` reverses that. Tracked in a local SQLite manifest. Produces a second binary (`bin/rhino`). **Not yet implemented**: splitting one file's bytes across multiple accounts — today each file is one upload to one account (see "Working guidelines" below).

See [README.md](README.md) for a full walkthrough of how the pieces fit together, the file structure, and how to run it.

## Stack & conventions

- **Language/module**: Go, module `github.com/Souvik-223/rhino-framework` (`go.mod` declares `go 1.25.0`, bumped automatically when the Google API client libraries were added — was `1.24.1`). `main.go`/`server.go` (repo root) are `package main`; `p2p/`, `storage/`, `drivepool/` (+ its subpackages) are separate importable packages; `cmd/rhino/` is a second `package main` producing the `rhino` binary.
- **Constructor pattern**: every major type follows `type XOpts struct {...}` + `func NewX(opts XOpts) *X` with the opts struct embedded directly into the returned struct (e.g. `type Store struct { StoreOpts }`, `type TCPTransport struct { TCPTransportOpts; ... }`, `type FileServer struct { FileServerOpts; ... }`). `drivepool.Pool`/`drivepool.Account` deviate slightly (constructed via `Open(ctx, ...)`/`AddAccount(ctx, ...)` rather than an Opts struct, since they need context + I/O to build) — follow whichever fits: Opts-struct for pure value construction, an `Open`/context-taking constructor when the type must do I/O (network calls, file/DB opens) to come into existence.
- **Interfaces over concretions**: `p2p.Transport`/`p2p.Peer` (`p2p/transport.go`) and `gdrive.RemoteStore` (`drivepool/gdrive/client.go`) are the same pattern applied twice — a small interface in its own package, one real implementation (`TCPTransport`, `gdrive.Client`), so core logic (`server.go`, `drivepool.Pool`) can be unit-tested against a fake with no real network/API calls (see `drivepool/pool_test.go`'s `fakeStore`). A new transport or a mock `RemoteStore` should satisfy the relevant interface and live in `p2p/`/`drivepool/gdrive`, not be special-cased in `server.go`/`pool.go`.
- **Serialization**: `encoding/gob` for `Message`/`RPC` payloads. Since `Message.Payload` is typed `any`, every concrete payload type must be registered with `gob.Register(...)` in the `init()` in `server.go` — remember to add new message types there.
- **Streaming vs. discrete messages**: raw file bytes are never gob-encoded. A one-byte marker (`p2p.IncomingMessage` / `p2p.IncomingStream` in `p2p/message.go`) tells the TCP read loop whether to decode the next bytes as an `RPC` or treat them as a stream to hand off directly (see `TCPTransport.handleConn` and the `sync.WaitGroup`-based `peer.CloseStream()`/`wg.Wait()` handshake).
- **Content addressing**: `storage.CASPathTransformFunc` (`storage/store.go`) — SHA-1 hash of the key, hex-encoded, split into 5-character directory segments. Don't bypass this with flat filenames when adding storage paths; use `PathTransformFunc` consistently. `drivepool` uses a SHA-256 content hash (not SHA-1) to name each file's remote Drive folder, since that hash also doubles as the integrity-check/future-dedup key — a stronger hash is warranted there.
- **Encryption**: `storage.CopyEncrypt`/`storage.CopyDecrypt` (`storage/crypto.go`) — AES-256-CTR, random IV per call, IV prepended to ciphertext. `storage.HashKey` (MD5) is only for obfuscating storage keys on the wire, not a security boundary — don't repurpose it for anything security-sensitive. Both `server.go` (P2P) and `drivepool/pool.go` (Drive pooling) share this same pipeline via the `storage` package rather than each having their own copy.
- **Concurrency**: peer map guarded by `sync.Mutex` (`FileServer.peerLock`); per-peer stream synchronization via `sync.WaitGroup`; RPCs delivered over a buffered channel (`Transport.Consume() <-chan RPC`). Keep new concurrent state behind an explicit mutex or channel rather than ad hoc synchronization. `drivepool.Manifest` opens SQLite with `SetMaxOpenConns(1)` since `modernc.org/sqlite` isn't safe for concurrent writers on one `*sql.DB` — don't raise that without adding real locking.
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
└── crypto_test.go        Test: encrypt -> decrypt round trip.
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
│                         Put (encrypt+stage+upload) / Get (download+decrypt+verify), List, Remove.
├── placement.go          pickAccount — least-used-first placement across healthy accounts.
├── pool_test.go          Tests against a fake in-memory RemoteStore (no real Drive/network calls).
├── auth/
│   ├── consent.go          RunConsentFlow — OAuth2 loopback-redirect consent flow (OOB is deprecated).
│   └── tokenstore.go        TokenStore — per-account token persistence (0600 files).
├── gdrive/
│   └── client.go            RemoteStore interface + Client — Drive API v3 wrapper.
└── manifest/
    ├── schema.go             SQLite DDL: accounts, virtual_files, chunks, chunk_replicas.
    └── manifest.go           Manifest — typed queries/inserts over database/sql (modernc.org/sqlite).

cmd/rhino/
└── main.go               cobra CLI: account add/list/remove, put, get, ls, rm, status.
                          Builds bin/rhino, separate from the P2P side's bin/fs.

bin/fs                  Build output of `make build` — currently checked into git (no .gitignore).
Makefile                build / run / test / build-rhino / run-rhino targets.
go.mod / go.sum          Module + dependencies. Google API/OAuth/SQLite/cobra libs added for drivepool;
                        testify et al. remain test-only, marked indirect.
```

## Commands

```bash
make build        # go build -o bin/fs
make run          # build, then run ./bin/fs (listens on TCP :3000)
make build-rhino  # go build -o bin/rhino ./cmd/rhino
make run-rhino    # build, then run ./bin/rhino
make test         # go test ./... -v
```

There's no linter configured — at minimum run `go vet ./...` and `gofmt -l .` after non-trivial changes (note: `gofmt -l .` will flag most pre-existing files as needing formatting purely due to CRLF line endings on this Windows checkout, not real style issues — check `gofmt -d <file>` before "fixing" one of those). No CI config and no `.env`/config file exist for the P2P side. The Drive pool's local state (SQLite manifest + OAuth tokens + client secret) lives under the OS config dir (`%APPDATA%\rhino` on Windows), not in the repo. A `.gitignore` exists and excludes `CLAUDE.md`, `AGENTS.md`, `.claude/`, `.agent/`, and `/tasks` — these are personal/local files, not part of the shared repo.

## Working guidelines

- This is a systems/networking project, not a web app — match idiomatic Go and the patterns already in the codebase (options-struct constructors, interface-based transport, explicit mutexes/channels for concurrency), not JS/TS or framework conventions.
- New transport implementations belong in `p2p/` and must satisfy `p2p.Transport`/`p2p.Peer`; don't add transport-specific branching into `server.go`.
- New gob-serialized message payload types must be registered in the `init()` in `server.go` or decoding will fail at runtime with no compile-time warning.
- `main.go` is a bare transport smoke-test today, not the wired-up file server — if you extend it into a real CLI, wire an actual `FileServer` (per the example in [README.md](README.md#running-multiple-nodes-manual-wiring)) rather than hand-rolling networking logic directly in `main.go`.
- **`drivepool` does not yet split one file across multiple accounts** — `Pool.Put` uploads the whole encrypted file as a single chunk (`chunks.idx = 0`) to whichever one account has the most free space. The `chunks`/`chunk_replicas` schema already supports multiple chunks/replicas per virtual file; implementing the actual split (chunk boundaries, per-chunk placement, parallel upload/download, reassembly by offset) is tracked as Phase 3 in `tasks/modification_plan.md` — don't assume it exists when reasoning about capacity (a file is still capped by one account's free space today) or when asked to add features that depend on it (e.g. replication, resumable partial uploads).
- Test `drivepool` core logic (placement, Put/Get) against the fake `gdrive.RemoteStore` in `drivepool/pool_test.go`, not real Drive calls — there are no live Google credentials in this environment, and the fake already exercises the full encrypt→stage→upload→download→decrypt→verify path.
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
