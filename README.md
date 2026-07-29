# RIHNO Framework 🦏

Two related distributed-storage subsystems, sharing one on-disk storage/encryption layer:

1. **A peer-to-peer, content-addressable file store** (`server.go`, `p2p/`) — nodes talk to each other over raw TCP, store file content on local disk using a SHA-1 content-addressable path layout, and encrypt every file's bytes with AES-256-CTR before they're written to disk or sent across the wire. A node that doesn't have a file locally can ask its peers for it and stream it back on demand.
2. **A Google-Drive-pooled storage CLI** (`drivepool/`, `cmd/rhino`) — pools multiple free Google Drive accounts (15GB each) into one virtual storage volume. Each file is encrypted client-side and uploaded to whichever registered account currently has the most free space, tracked in a local SQLite manifest. See "Google Drive pooling" below.

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
- No CI, no linter config, no `.env`/config file support for the P2P side — everything is set via Go struct literals in code (see "Running multiple nodes" below).
- **The Google Drive pool does not yet split one file's bytes across multiple accounts.** `rhino put` uploads each file as a single encrypted blob to whichever account currently has the most free space — so today, one file is still capped by that one account's remaining quota, even though *different* files get spread across accounts. True chunking (splitting a single large file across N accounts so it can exceed any one account's free space) is designed but not yet built — see `tasks/modification_plan.md`, Phase 3.

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
├── pool.go                Pool / Account — registers accounts, encrypts+uploads (Put), downloads+
│                          decrypts+verifies (Get), lists/removes virtual files.
├── placement.go           pickAccount — least-used-first placement across healthy accounts.
├── pool_test.go           Tests against a fake in-memory RemoteStore (no real Drive calls).
├── auth/
│   ├── consent.go           RunConsentFlow — OAuth2 loopback-redirect consent flow.
│   └── tokenstore.go         TokenStore — per-account token persistence (0600 files).
├── gdrive/
│   └── client.go             RemoteStore interface + Client — Drive API v3 wrapper
│                              (EnsureFolder/Upload/Download/Delete/Quota).
└── manifest/
    ├── schema.go              SQLite DDL: accounts, virtual_files, chunks, chunk_replicas.
    └── manifest.go            Manifest — typed queries/inserts over database/sql.

cmd/rhino/
└── main.go                cobra CLI: account add/list/remove, put, get, ls, rm, status.

bin/fs                  Build output of `make build` (currently checked into git).
Makefile                 build / run / test / build-rhino / run-rhino targets.
go.mod / go.sum          Module github.com/Souvik-223/rhino-framework, Go 1.25.
```

## Requirements

- Go 1.25 or newer (`go.mod` currently declares `go 1.25.0`, bumped automatically when the Google API client libraries were added).
- The P2P side needs no database, environment variables, or external services.
- The Google Drive pool needs a one-time Google Cloud OAuth setup (free) — see "Google Drive pooling" below. Its local state (SQLite manifest + OAuth tokens) lives under your OS config dir (e.g. `%APPDATA%\rhino` on Windows), not in this repo.

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
make test          # go test ./... -v
```

Covers: CAS path-sharding + store read/write/delete (`storage/store_test.go`), AES encrypt/decrypt round trip (`storage/crypto_test.go`), a TCP transport smoke test (`p2p/tcp_transport_test.go`), and the full `drivepool` `Put`/`Get` pipeline — encrypt → stage → upload → download → decrypt → verify — plus placement logic, both tested against a fake in-memory Drive account (`drivepool/pool_test.go`) so no real Google credentials are needed to run them. All pass on both Windows and Unix.

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

`drivepool` treats N registered Google Drive accounts as one pool: `rhino put` encrypts a file with AES-256-CTR (via the same `storage.CopyEncrypt` used by the P2P side), stages the ciphertext locally, then uploads it to whichever registered account currently has the most free space (a live `About.get` quota check per account, not a cached guess). `rhino get` reverses this — downloads, decrypts, and verifies the plaintext against a SHA-256 content hash recorded at upload time. Everything is tracked in a local SQLite manifest (`accounts` / `virtual_files` / `chunks` tables) so `rhino ls`/`status` can report on the pool without touching the network.

**Not yet implemented:** splitting one file into many chunks spread across accounts (today each file is one upload to one account) and its follow-ons (replication, dedup, versioning). See `tasks/modification_plan.md` for the full phased design — this is Phase 1+2 of that plan.

### One-time setup (per machine, free)

1. Go to [console.cloud.google.com](https://console.cloud.google.com), create a project, and enable the **Google Drive API**.
2. **APIs & Services → OAuth consent screen**: User type **External**, add scope `https://www.googleapis.com/auth/drive.file`, then **publish the app to "In production"**. This one click-through matters: apps left in "Testing" status get refresh tokens that expire after 7 days, forcing weekly re-auth per account. `drive.file` is a non-sensitive scope, so publishing doesn't require Google's full app-verification review — you'll just see (and can dismiss) an "unverified app" warning once per account during consent, which is expected for a personal, non-public tool.
3. **APIs & Services → Credentials → Create Credentials → OAuth client ID → Desktop app**. Download the JSON.
4. Save that file as `<config dir>/rhino/client_secret.json` — on Windows, `%APPDATA%\rhino\client_secret.json` (or pass `--credentials <path>` to every `rhino` command instead).

### Usage

```bash
make build-rhino          # or: go build -o bin/rhino ./cmd/rhino (bin/rhino.exe on Windows PowerShell)

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
