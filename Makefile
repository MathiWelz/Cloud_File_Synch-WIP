##############################################################################
# CloudSync – Makefile
# Targets: build, install, cross-compile for Windows/Linux/macOS
##############################################################################

APP     := cloudsync
VERSION := 1.0.0
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: all build install clean cross tidy

## Default target
all: tidy build

## Download / tidy dependencies
tidy:
	go mod tidy

## Build for the current OS/arch
build:
	go build $(LDFLAGS) -o $(APP) .

## Install to $GOPATH/bin (or ~/go/bin)
install:
	go install $(LDFLAGS) .

## Remove compiled binaries
clean:
	rm -f $(APP) $(APP).exe
	rm -f dist/$(APP)-*

## Cross-compile for Linux, macOS, and Windows (amd64 + arm64)
cross:
	mkdir -p dist
	GOOS=linux   GOARCH=amd64  go build $(LDFLAGS) -o dist/$(APP)-linux-amd64     .
	GOOS=linux   GOARCH=arm64  go build $(LDFLAGS) -o dist/$(APP)-linux-arm64     .
	GOOS=darwin  GOARCH=amd64  go build $(LDFLAGS) -o dist/$(APP)-darwin-amd64    .
	GOOS=darwin  GOARCH=arm64  go build $(LDFLAGS) -o dist/$(APP)-darwin-arm64    .
	GOOS=windows GOARCH=amd64  go build $(LDFLAGS) -o dist/$(APP)-windows-amd64.exe .
	@echo "\n✅ Cross-compile complete. Binaries in ./dist/"

## Run (pass config path as CONFIG=path/to/config.yaml)
run:
	go run . $(CONFIG)

## Run tests
test:
	go test ./...
