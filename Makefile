# wherenv — Makefile

BINARY      := wherenv
PKG         := ./cmd/wherenv
BIN_DIR     := bin
BIN         := $(BIN_DIR)/$(BINARY)
LDFLAGS     := -s -w

.DEFAULT_GOAL := build

.PHONY: build install test clean vet fmt tidy lint all

## build: compile the binary into ./bin
build:
	@mkdir -p $(BIN_DIR)
	go build -ldflags '$(LDFLAGS)' -o $(BIN) $(PKG)

## install: install the binary into $(go env GOBIN) (or GOPATH/bin)
install:
	go install -ldflags '$(LDFLAGS)' $(PKG)

## test: run all tests with the race detector
test:
	go test -race -count=1 ./...

## vet: run go vet
vet:
	go vet ./...

## fmt: format all Go sources
fmt:
	gofmt -s -w .

## tidy: tidy go.mod / go.sum
tidy:
	go mod tidy

## all: format, vet, test, build
all: fmt vet test build

## clean: remove build artifacts and the test cache
clean:
	rm -rf $(BIN_DIR)
	go clean -testcache
