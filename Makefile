BINARY  := lantern
PKG     := ./cmd/lantern
BIN_DIR := bin
VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

export CGO_ENABLED := 0

.PHONY: build run test vet fmt build-mac build-linux clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(PKG)

run: build
	./$(BIN_DIR)/$(BINARY)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

build-mac:
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY)-darwin-arm64 $(PKG)

build-linux:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY)-linux-amd64 $(PKG)

clean:
	rm -rf $(BIN_DIR)
