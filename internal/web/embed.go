package web

import "embed"

// Assets contains the desktop-paradigm console (web/ is the editable source).
//
//go:embed index.html app.js styles.css
var Assets embed.FS
