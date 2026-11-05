// Package version exposes the binary's version string. Set at build time
// via -ldflags "-X github.com/dongsheng123132/remote-agent/internal/version.Version=v1.2.3".
// Defaults to "dev" so unset binaries are obviously identifiable in
// `remote-agent list` output.
package version

var Version = "dev"
