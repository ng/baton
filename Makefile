.PHONY: build build-mac build-mac-intel build-linux build-tray build-tray-go build-all test clean install-remote

BINARY := baton
VERSION := 0.1.0
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

build-mac:
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY)-darwin-arm64 .

build-mac-intel:
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY)-darwin-amd64 .

build-linux:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY)-linux-amd64 .

build-tray-go:
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build $(LDFLAGS) -o $(BINARY)-tray-darwin-arm64 ./cmd/baton-tray/

build-tray:
	cd BatonTray && swift build -c release
	cp -f BatonTray/.build/release/BatonTray ./BatonTray.app 2>/dev/null || true

build: build-mac

build-all: build-mac build-mac-intel build-linux build-tray

test:
	go test ./...

clean:
	rm -f $(BINARY)-*
	rm -f BatonTray.app
	rm -rf BatonTray/.build

install-remote:
	@echo "Installing remote shell integration..."
	mkdir -p /workspaces/.inbox
	cp -f remote/grab.sh /usr/local/bin/grab
	chmod +x /usr/local/bin/grab
	@echo "Source remote/baton.plugin.zsh in your .zshrc for auto-detect"
