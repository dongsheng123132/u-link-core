.PHONY: all server agent cli ulink link link-windows link-package-mac test tidy
MODULE := github.com/dongsheng123132/remote-agent
VERSION ?= dev
GOFLAGS := -trimpath -ldflags "-s -w -X $(MODULE)/internal/version.Version=$(VERSION)"
all: server agent cli ulink
tidy:
	go mod tidy
test:
	go test ./...
server: tidy
	go build $(GOFLAGS) -o build/remote-agent-server ./cmd/server
agent: tidy
	go build $(GOFLAGS) -o build/ulink-agent ./cmd/agent
cli: tidy
	go build $(GOFLAGS) -o build/remote-agent ./cmd/cli
ulink: tidy
	go build $(GOFLAGS) -o build/ulink ./cmd/ulink

# Desktop app: macOS menu bar / Windows tray.  The macOS build needs cgo
# (Xcode Command Line Tools); the Windows build is pure Go.
link: tidy
	CGO_ENABLED=1 go build $(GOFLAGS) -o build/u-link ./cmd/link

link-windows: tidy
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath \
	  -ldflags "-s -w -H windowsgui -X $(MODULE)/internal/version.Version=$(VERSION)" \
	  -o build/u-link.exe ./cmd/link

# Wrap build/u-link into build/U-Link.app + a distributable zip (macOS only).
link-package-mac: link
	VERSION=$(VERSION) bash scripts/package-mac-link.sh $(VERSION)
