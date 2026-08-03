build:
	@go build -o bin/fs

run: build
	@./bin/fs

build-rhino:
	@go build -o bin/rhino ./cmd/rhino

run-rhino: build-rhino
	@./bin/rhino

test:
	@go test ./... -v

vet:
	@go vet ./...

# Builds frontend/ and copies its output into backend/dist/ — go:embed can
# only reach files inside backend/'s own directory, not a sibling like
# frontend/dist, so this copy step is required before `go build` embeds it.
build-frontend:
	@npm --prefix frontend ci
	@npm --prefix frontend run build
	@go run ./tools/copydist frontend/dist backend/dist

build-portal: build-frontend
	@go build -o bin/rhino ./cmd/rhino

run-portal: build-portal
	@./bin/rhino serve