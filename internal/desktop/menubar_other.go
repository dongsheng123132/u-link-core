//go:build !darwin

package desktop

// The macOS 26 menu bar layout quirk does not exist on other platforms.
func ensureMenuBarVisibleOnce(configDir string) {}
