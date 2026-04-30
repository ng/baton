.PHONY: build build-mac build-linux clean

BINARY := shuttle
VERSION := 0.1.0
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

build-mac:
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY)-darwin-arm64 .

build-mac-intel:
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY)-darwin-amd64 .

build-linux:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY)-linux-amd64 .

build: build-mac

clean:
	rm -f $(BINARY)-*

install-remote:
	@echo "Installing remote shell integration..."
	mkdir -p /workspaces/.inbox
	cp -f remote/grab.sh /usr/local/bin/grab
	chmod +x /usr/local/bin/grab
	@echo "Source remote/shuttle.plugin.zsh in your .zshrc for auto-detect"
