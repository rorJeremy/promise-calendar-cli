# pcal development tasks. `make` runs `check`, the same gate CI runs (as in
# fizzy-cli). Tools come from mise.toml; in a shell without mise's prompt hook
# (an agent, a script), run `mise exec -- make …`.

.DEFAULT_GOAL := check

# The calendar app's checkout, for `make golden`.
RAILS ?= ../promise-church-calendar

.PHONY: build test vet lint fmt fmt-check tidy-check check golden clean

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

## golden: replace testdata/api/ with the calendar app's golden JSON, the
## API contract (make golden RAILS=../promise-church-calendar). Manual: check
## and CI never need the app. Files the app dropped are dropped here too.
golden:
	@test -d "$(RAILS)/test/fixtures/files/api" || \
		(echo "No $(RAILS)/test/fixtures/files/api. Run: make golden RAILS=<path to promise-church-calendar>"; exit 1)
	rm -f testdata/api/*.json
	mkdir -p testdata/api
	cp "$(RAILS)"/test/fixtures/files/api/*.json testdata/api/
	@ls testdata/api/*.json | wc -l | xargs echo "Copied golden files:"

## clean: remove build output
clean:
	rm -rf bin/ dist/ completions/
