.PHONY: all server agent cli ulink test tidy
MODULE := github.com/dongsheng123132/remote-agent
GOFLAGS := -trimpath -ldflags "-s -w"
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
