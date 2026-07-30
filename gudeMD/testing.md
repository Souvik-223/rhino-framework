# Testing Guide — RIHNO Framework

Everything in this repo that can be tested, and exactly how to test it: the
original P2P file store, the Drive-pool CLI (including chunking), the web
portal backend and frontend, and the Docker deployment. Commands below were
all actually run against this codebase (not generic advice) — see
`plans/web_portal.md` for the design rationale behind any of it.

## Contents

1. [Prerequisites](#1-prerequisites)
2. [Quick start — verify everything in one pass](#2-quick-start--verify-everything-in-one-pass)
3. [Automated test suites](#3-automated-test-suites)
4. [Manual testing — P2P file store](#4-manual-testing--p2p-file-store)
5. [Manual testing — Drive-pool CLI](#5-manual-testing--drive-pool-cli)
6. [Manual testing — web portal](#6-manual-testing--web-portal)
7. [Docker / docker-compose testing](#7-docker--docker-compose-testing)
8. [CI — what runs automatically](#8-ci--what-runs-automatically)
9. [Capability checklist](#9-capability-checklist)
10. [Troubleshooting](#10-troubleshooting)

---

## 1. Prerequisites

| Need | Required for |
| --- | --- |
| Go 1.25+ | Everything |
| Node.js 22+ / npm | Building or testing `frontend/` |
| `make` (git-bash/WSL) | Optional — every `make` target has a raw-command equivalent below |
| A real Google Cloud OAuth `client_secret.json` | Only for *actually connecting a Drive account and moving real files* — every automated test and most manual API/UI testing works without one |
| Docker + Docker Compose | Only for §7 |
| A C compiler (gcc/clang) | Only for `go test -race` |

Nothing else needs installing — `modernc.org/sqlite` is pure Go (no CGO), and
every dependency is fetched by `go mod download` / `npm install`.

> **No `make` available?** Every target below is a one-liner; the raw
> command is always shown alongside it.

---

## 2. Quick start — verify everything in one pass

```bash
# Go side: build, vet, test every package
go build ./... && go vet ./... && go test ./... -v

# Frontend: type-check, build, test
cd frontend && npm ci && npm run build && npm run test && cd ..
```

If both of those succeed, the entire codebase — P2P store, drivepool
(including chunking), the backend API, and the frontend — is in a known-good
state. Everything past this point is either *deeper* automated coverage
detail or *manual* exercising of the running system.

---

## 3. Automated test suites

### 3.1 Go tests

```bash
go test ./... -v          # everything
go test ./drivepool/... -v          # just the Drive pool + chunking
go test ./backend/... -v            # just the web portal API
go test ./storage/... ./p2p/... -v  # just the P2P side
```

| Package | What it actually covers |
| --- | --- |
| `storage` | CAS path-sharding (`store_test.go`), write/read/has/delete round trip, AES-256-CTR encrypt→decrypt round trip (`crypto_test.go`) |
| `p2p` | `TCPTransport` dial/accept smoke test |
| `drivepool` (`pool_test.go`) | Placement (`pickAccount` picks the most-free healthy account, skips unhealthy ones, errors when none are healthy), full `Put`/`Get` round trip against a fake in-memory Drive account |
| `drivepool` (`chunking_test.go`) | A file larger than `ChunkSize` actually splits into multiple chunks; those chunks land on **different** accounts (not all on one); a mid-upload chunk failure triggers best-effort cleanup of the chunks that *did* upload, and leaves no manifest record; a chunk whose stored ciphertext is tampered with post-upload is detected and `GetStream` fails rather than silently returning corrupted data |
| `backend/tests` | Register → `/me` → logout → `/me` now 401; duplicate-username registration is rejected (409); wrong password is rejected (401); `/api/accounts`/`/api/files` require a session (401 without one); full upload → list → download → delete round trip over real HTTP via the real Gin router; `/healthz`/`/readyz` |

None of these touch the real network or a real Google account — `drivepool`
and `backend/tests` both run against a fake in-memory `gdrive.RemoteStore`.

**Race detector**: `go test ./... -race` needs CGO + a real C compiler
(`CGO_ENABLED=1` requires gcc/clang on `PATH`). If that's not available, skip
`-race` locally and rely on `go vet` + careful review of the concurrent code
in `drivepool/pool.go` (`PutStream`/`GetStream`) and `backend/poolcache.go`.

### 3.2 Frontend tests

```bash
cd frontend
npm run test        # vitest run
```

| File | Covers |
| --- | --- |
| `tests/composables/useBytes.test.ts` | `formatBytes` output at every unit (B/KiB/MiB/GiB), `usageLevel`'s 70%/90% thresholds |
| `tests/stores/auth.test.ts` | Login success sets `username`; a 401 leaves it `null` and throws; `checkSession` marks `checked=true`; logout clears `username` |
| `tests/api/client.test.ts` | 2xx responses parse as JSON; non-2xx responses throw `ApiError` with the server's message (even when the error body isn't JSON); 204 resolves to `undefined` |
| `tests/components/FileGrid.test.ts` | Empty state, a populated file row with correctly formatted size, an in-progress upload's progress bar |
| `tests/components/DropZone.test.ts` | The drop overlay appears on `dragenter` and disappears after `drop`; dropped files are handed to the files store |

All network calls are mocked (`vi.stubGlobal('fetch', ...)`) — no backend
needs to be running for these.

---

## 4. Manual testing — P2P file store

```bash
go build -o bin/fs .      # or: make build
./bin/fs                  # or: make run
```

Starts a bare `TCPTransport` on `:3000` that logs any `RPC` it receives —
`main.go` doesn't wire up a real `FileServer` yet (see README's "Current
status"), so this only proves the transport itself is listening:

```bash
nc localhost 3000
hello                     # type this and press enter; ./bin/fs's log should show it
```

To actually exercise `FileServer.Store`/`Get` replication across peers, use
the manual wiring snippet in the README's ["Running multiple nodes"](../README.md#running-multiple-nodes-manual-wiring)
section — it builds 3 in-process nodes, stores a file on one, and reads it
back from another to prove replication worked.

---

## 5. Manual testing — Drive-pool CLI

### 5.1 Without any real Google credentials

Every CLI command *except* `account add` (and anything that needs a
registered account) works with zero setup:

```bash
go build -o bin/rhino ./cmd/rhino     # or: make build-rhino
./bin/rhino account list               # "no accounts registered yet — try: rhino account add --label <name>"
./bin/rhino ls                          # "no files stored yet"
./bin/rhino status                      # "0/0 accounts healthy | 0 B total | 0 B free | 0 files"
```

`account add`, `put`, `get`, `ls`, `status` on a pool with accounts all
require a `client_secret.json` because `openPool()` always loads the OAuth
client config up front (see §10 if you hit that error and don't have one yet
— it's not a bug, it's how the CLI has always worked).

### 5.2 With real Google credentials — the full flow

One-time setup: follow the README's ["One-time setup"](../README.md#one-time-setup-per-machine-free)
section (create a Google Cloud project, enable the Drive API, publish the
OAuth consent screen, download `client_secret.json` to
`%APPDATA%\rhino\client_secret.json`).

```bash
./bin/rhino account add --label home-gmail   # opens a browser for consent
./bin/rhino account add --label work-gmail   # register a second account, to actually exercise placement
./bin/rhino account list
#   home-gmail           14.8 GiB free of 15.0 GiB
#   work-gmail            9.2 GiB free of 15.0 GiB
```

**Testing single-chunk put/get** (small file, stays on one account):

```bash
./bin/rhino put ~/photos/trip.jpg --as photos/trip.jpg
./bin/rhino ls
./bin/rhino get photos/trip.jpg ~/restored.jpg
diff ~/photos/trip.jpg ~/restored.jpg   # should be identical
```

**Testing real multi-account chunking** — this is the part that's hardest to
verify without real accounts, since the automated tests use a fake store
with an artificially small `ChunkSize`. To actually see a file split across
your two real registered accounts, you need a file bigger than
`drivepool.DefaultChunkSize` (128 MiB):

```bash
# create a ~300MiB test file (3 chunks at the default 128MiB chunk size)
head -c 300000000 /dev/urandom > /tmp/bigfile.bin   # git-bash/WSL
# PowerShell equivalent:
#   fsutil file createnew bigfile.bin 300000000

./bin/rhino put /tmp/bigfile.bin --as test/bigfile.bin
./bin/rhino status
#   both accounts' "usage" should have grown — proof the chunks actually
#   landed on more than one account, not all on whichever was picked first

./bin/rhino get test/bigfile.bin /tmp/restored.bin
diff /tmp/bigfile.bin /tmp/restored.bin   # must be identical byte-for-byte
./bin/rhino rm test/bigfile.bin --purge   # clean up the real remote chunks
```

If `diff` matches, that's end-to-end proof of: chunking, concurrent
placement across real accounts, concurrent upload, concurrent download, and
correct in-order reassembly — all against the real Drive API.

```bash
./bin/rhino rm photos/trip.jpg --purge
./bin/rhino account remove home-gmail   # deregisters locally, doesn't touch remote files
```

---

## 6. Manual testing — web portal

### 6.1 Dev mode (hot reload, two processes)

```bash
# terminal 1
go run ./cmd/rhino serve --addr :8080

# terminal 2
cd frontend && npm run dev
```

Open the URL Vite prints (typically `http://localhost:5173`) — it proxies
`/api/*` to the Go server on `:8080` (configured in `frontend/vite.config.ts`).
Edit any `.vue` file and the browser updates instantly.

### 6.2 Production build mode (single embedded binary)

```bash
make build-frontend   # npm ci + npm run build, then copies frontend/dist -> backend/dist
make build-portal      # then: go build -o bin/rhino ./cmd/rhino
make run-portal         # then: ./bin/rhino serve

# raw commands, if make isn't available:
npm --prefix frontend ci
npm --prefix frontend run build
rm -rf backend/dist && cp -r frontend/dist backend/dist
go build -o bin/rhino ./cmd/rhino
./bin/rhino serve
```

Open `http://localhost:8080/` — you should get the **real Vue app**, not the
placeholder page. (If you see a plain "Placeholder —" page, the copy step
above didn't run — `backend/dist/` still has the checked-in placeholder.)

### 6.3 Full walkthrough — register through delete

Needs `client_secret.json` in place for the account-connect and file steps
(same one-time setup as §5.2); registration/login/health work without it.

1. Open `http://localhost:8080/register`, create an account.
2. You land on the dashboard — empty sidebar, empty file list.
3. Click **"+ Connect account"**, type a label, submit — a real OAuth consent
   tab opens (same flow as `rhino account add`). After approving, the
   sidebar shows the account with a usage bar.
4. Drag a file anywhere onto the page — the full-page "Drop files here"
   overlay should appear on drag-enter and the file should start uploading
   (progress bar in the file list) as soon as you drop it.
5. Click **Download** on the uploaded file — it should download with its
   original name and byte-identical content.
6. Click **Delete** — a confirm dialog offers purge (delete remote chunks
   too) vs. keep; either way the row disappears from the list.
7. Click **Log out**, then reload `/` — you should be redirected to
   `/login`, not shown the dashboard.

### 6.4 Testing multi-tenancy (two users don't see each other's data)

Register two different users (e.g. in two separate browser profiles, or one
normal + one incognito window, since sessions are cookie-based). Connect a
different Drive account label to each, upload a file under each. Confirm
neither user's sidebar/file list ever shows the other's account or files —
this is the entire tenant-isolation boundary (`backend/middleware.go` derives
`userID` only from the verified session, never from anything client-supplied).

### 6.5 API-level testing without a browser

Useful for isolating backend issues from frontend ones:

```bash
# health checks — no auth needed
curl -i http://localhost:8080/healthz
curl -i http://localhost:8080/readyz

# register + capture the session cookie
curl -c cookies.txt -X POST http://localhost:8080/api/auth/register \
  -H "Content-Type: application/json" \
  -d '{"username":"alice","password":"a-fine-password"}'

# authenticated requests, reusing the cookie
curl -b cookies.txt http://localhost:8080/api/me
curl -b cookies.txt http://localhost:8080/api/accounts
curl -b cookies.txt http://localhost:8080/api/files

# upload
curl -b cookies.txt -F "file=@/path/to/some/file.txt" http://localhost:8080/api/files

# download
curl -b cookies.txt "http://localhost:8080/api/files/download?name=file.txt" -o downloaded.txt

# delete (purge=true also deletes the remote chunks)
curl -b cookies.txt -X DELETE "http://localhost:8080/api/files?name=file.txt&purge=true"

# without the cookie, everything past register/login should 401
curl -i http://localhost:8080/api/files
```

**Testing without real Google credentials at all** (register/login/health
only — `/api/accounts`/`/api/files` need *some* `client_secret.json` to exist
under `RHINO_DATA_DIR`, even a structurally-valid dummy one, since opening a
user's Pool loads the OAuth client config regardless of whether any account
is actually connected yet):

```bash
mkdir -p /tmp/rhino-test-data
cat > /tmp/rhino-test-data/client_secret.json <<'EOF'
{"installed":{"client_id":"dummy.apps.googleusercontent.com","client_secret":"dummy","redirect_uris":["http://localhost"],"auth_uri":"https://accounts.google.com/o/oauth2/auth","token_uri":"https://oauth2.googleapis.com/token"}}
EOF
export RHINO_DATA_DIR=/tmp/rhino-test-data
export RHINO_SESSION_SECRET="local-test-secret-at-least-this-long"
./bin/rhino serve
```

With zero accounts connected, `/api/accounts` and `/api/files` return `[]`
without ever making a real network call — good enough to verify the whole
auth+routing+session stack works before you have real Drive credentials.

---

## 7. Docker / docker-compose testing

```bash
docker build -t rhino .
docker run --rm -p 8080:8080 \
  -e RHINO_DATA_DIR=/data \
  -e RHINO_SESSION_SECRET=local-docker-test-secret-32bytes \
  -v rhino-test-data:/data \
  rhino
curl -i http://localhost:8080/healthz
```

Full stack with the Caddy reverse proxy (requires real secrets files first):

```bash
mkdir -p secrets
echo -n "$(openssl rand -base64 32)" > secrets/session_secret.txt
cp /path/to/client_secret.json secrets/client_secret.json
# edit Caddyfile's placeholder domain before this will actually get a cert

docker compose up -d --build
docker compose logs -f rhino
docker compose down
```

Things worth specifically checking here that don't show up in local testing:
- The image contains no Node/Go toolchain (`docker run --rm rhino sh` should
  fail — the runtime image is `distroless/static`, there's no shell).
- Data survives a restart: `docker compose restart rhino` then confirm
  previously-registered users/files are still there (proves the volume
  mount is working, not just in-container state).
- Graceful shutdown: `docker compose stop rhino` shouldn't drop an in-flight
  request abruptly — `cmd/rhino/serve.go`'s `signal.NotifyContext` +
  `http.Server.Shutdown` should drain it first (harder to observe directly,
  but a `docker compose stop` that returns quickly with no error in the logs
  is a good sign; an actual `context deadline exceeded`/panic in the logs
  would indicate the shutdown path is broken).

---

## 8. CI — what runs automatically

Nothing below needs to be run manually if you push to GitHub — it's here so
you know what's already covered before you look for it yourself.

`.github/workflows/ci.yml` — every push/PR:
```
go vet ./...
go build ./...
go test ./... -v
npm --prefix frontend ci
npm --prefix frontend run build
npm --prefix frontend run test
```

`.github/workflows/release.yml` — on pushing a `v*` tag: builds the
multi-stage Docker image and pushes it to `ghcr.io/<repo>` tagged with the
version and `latest`.

---

## 9. Capability checklist

| Capability | Verify with |
| --- | --- |
| TCP transport listens/accepts | `go test ./p2p/...` or §4's `nc` smoke test |
| Content-addressable storage (SHA-1 sharded paths) | `go test ./storage/...` |
| AES-256-CTR encrypt/decrypt round trip | `go test ./storage/...` |
| P2P multi-node file replication | README's manual wiring snippet |
| Register/list/remove Drive accounts | §5.2 or §6.5 |
| Placement picks the most-free healthy account | `go test ./drivepool/...` (`TestPickAccountPrefersMostFreeSpace`) |
| Single-chunk put/get round trip | §5.2's small-file test, or `go test ./drivepool/...` |
| Multi-account chunking (file split across accounts) | §5.2's big-file test, or `go test ./drivepool/...` (`TestPutStreamSplitsAcrossAccountsAndRoundTrips`) |
| Concurrent chunk upload/download | Implicit in the above — inspect timing/logs, or read `drivepool/pool.go`'s `PutStream`/`GetStream` |
| Partial-upload-failure cleanup | `go test ./drivepool/...` (`TestPutStreamFailureCleansUpUploadedChunks`) — hard to trigger manually on purpose |
| Tampered-chunk detection | `go test ./drivepool/...` (`TestGetStreamDetectsTamperedChunk`) |
| Graceful degradation (one bad account doesn't break others) | Revoke one account's token/consent in your Google account settings, then confirm other commands still work and only that account reports "unavailable" |
| Portal registration/login/logout/session enforcement | §6.3 or §6.5, or `go test ./backend/...` |
| Multi-tenant isolation | §6.4 |
| Drag-and-drop upload + file-picker fallback | §6.3 (browser), or `npm run test` (`DropZone.test.ts`) |
| Download / delete (with purge option) | §6.3 or §6.5 |
| Usage bars / thresholds (green/amber/red) | §6.3 (browser) or `npm run test` (`useBytes.test.ts`) |
| `/healthz` / `/readyz` | §6.5, or `docker compose`'s health checks |
| Graceful shutdown on SIGTERM | §7 |
| Docker image builds and runs standalone | §7 |
| CI runs Go + frontend checks automatically | Push a commit/PR and check the Actions tab |
| Release builds & pushes a Docker image | Push a `v*` tag and check the Actions tab / GHCR |

---

## 10. Troubleshooting

**`open .../client_secret.json: no such file`** — expected until you've done
the one-time Google Cloud setup (§5.2). Everything else (registration,
login, health checks, and all automated tests) works without it; only real
account-connect/upload/download need it. A structurally-valid *dummy* JSON
(§6.5) is enough to unblock `/api/accounts`/`/api/files` with zero accounts
connected, without any real Google project.

**`go test -race` fails with "requires cgo"** — no C compiler on `PATH`
(`CGO_ENABLED=0` builds fine without one, but `-race` specifically needs
cgo). Install a C toolchain (e.g. `mingw-w64` on Windows) or run `-race` on
a machine that already has one; not required for normal `go test`.

**`make: command not found`** — every `make` target has a raw-command
equivalent shown right next to it in this doc; use those instead.

**PowerShell binary won't run (`./bin/rhino` not recognized)** — PowerShell
refuses to execute a binary without a `.exe` extension even though it's
valid. Build with `go build -o bin/rhino.exe ./cmd/rhino` and run
`.\bin\rhino.exe`. Git Bash doesn't have this restriction.

**Root page (`/`) shows the placeholder instead of the real UI** —
`backend/dist/` still has the checked-in placeholder `index.html`; run
`make build-frontend` (or its raw-command equivalent in §6.2) to copy the
real Vite build in before `go build`.

**A logged-in session doesn't survive a server restart** —
`RHINO_SESSION_SECRET` wasn't set, so a random key was generated at startup
(logged as a warning) and every session was invalidated when the process
restarted. Set `RHINO_SESSION_SECRET` (or `_FILE`) to a stable value.
