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