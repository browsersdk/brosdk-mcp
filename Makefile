## brosdk-mcp build & release helpers
## Usage: make <target>

MODULE   := brosdk-mcp
CMD      := ./cmd/brosdk-mcp
BINARY   := brosdk-mcp
DIST     := dist

# Version: prefer git tag, fall back to "dev"
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS  := -X main.Version=$(VERSION) -s -w

# Detect current OS: if GOOS is not set, infer from uname / OS env
# On Windows (cmd/PowerShell) the OS variable is "Windows_NT"; uname is unavailable.
GOOS     ?= $(shell go env GOOS)
ifeq ($(GOOS),windows)
  EXT    := .exe
else
  EXT    :=
endif
OUT      := $(BINARY)$(EXT)

.PHONY: all build test lint vet clean release help

all: build

## build – compile for the current platform (auto-detects OS, adds .exe on Windows)
build:
	go build -ldflags "$(LDFLAGS)" -o $(OUT) $(CMD)
	@echo "Built: $(OUT)"

## build-all – cross-compile for common release targets
build-all:
	GOOS=linux   GOARCH=amd64  go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-amd64   $(CMD)
	GOOS=linux   GOARCH=arm64  go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-arm64   $(CMD)
	GOOS=darwin  GOARCH=amd64  go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-darwin-amd64  $(CMD)
	GOOS=darwin  GOARCH=arm64  go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-darwin-arm64  $(CMD)
	GOOS=windows GOARCH=amd64  go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-windows-amd64.exe $(CMD)

## test – run all unit tests (no E2E)
test:
	go test ./...

## test-e2e – run E2E tests (requires Chrome)
test-e2e:
	BROSDK_E2E=1 go test ./internal/e2e/... -v -timeout 120s

## vet – run go vet
vet:
	go vet ./...

## clean – remove build artefacts
clean:
	rm -f $(OUT) $(BINARY) $(BINARY).exe
	rm -rf $(DIST)

## version – print current version
version:
	@echo $(VERSION)

## help – list available targets
help:
	@grep -E '^## ' Makefile | sed 's/## //'
