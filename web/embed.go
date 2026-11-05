// Package web holds the browser UI (HTML, CSS, ES modules, vendored libraries)
// and embeds it into the binary.
package web

import "embed"

// FS is the UI, rooted at index.html.
//
//go:embed index.html style.css theme-init.js js vendor
var FS embed.FS
