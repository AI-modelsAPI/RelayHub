package web

import "embed"

// Assets contains the management console assets.
//
//go:embed index.html app.js styles.css
var Assets embed.FS
