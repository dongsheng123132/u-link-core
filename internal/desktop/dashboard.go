package desktop

import _ "embed"

// dashboardHTML is the entire local control panel: one file, no CDN, no
// build step.  Served from 127.0.0.1 and opened from the tray menu.
//
//go:embed dashboard.html
var dashboardHTML []byte
