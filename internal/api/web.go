package api

import (
	"embed"
	"mime"
)

//go:embed all:web
var webFS embed.FS

func init() {
	// Go's built-in MIME table doesn't know .webmanifest; register it so the
	// PWA manifest is served as application/manifest+json rather than sniffed.
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}
