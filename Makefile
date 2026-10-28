# pcal development tasks. `make` runs `check`, the same gate CI runs (as in
# fizzy-cli). Tools come from mise.toml; in a shell without mise's prompt hook
# (an agent, a script), run `mise exec -- make …`.

.DEFAULT_GOAL := check

.PHONY: build test vet lint fmt fmt-check tidy-check check clean

## build: compile ./cmd/pcal into bin/pcal
build:
	go build -o bin/pcal ./cmd/pcal

## test: run every test with the race detector (needs cgo and a C compiler)
test:
	go test -race ./...

## vet: run go vet
vet:
	go vet ./...

## lint: run golangci-lint (config in .golangci.yml)
lint:
	golangci-lint run ./...

## fmt: format every Go file in place
fmt:
	gofmt -s -w .

## fmt-check: fail if any Go file isn't gofmt-formatted
fmt-check:
	@test -z "$$(gofmt -l .)" || (echo "Not gofmt-formatted (run 'make fmt'):"; gofmt -l .; exit 1)

## tidy-check: fail if go.mod or go.sum isn't tidy, without changing them
tidy-check:
	go mod tidy -diff

## check: everything CI checks
check: fmt-check vet lint tidy-check test

## clean: remove build output
clean:
	rm -rf bin/ dist/ completions/
