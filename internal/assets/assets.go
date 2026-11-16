// Package assets holds build-time embedded assets shared by the manager and the
// host-side fpk builder.
package assets

import _ "embed"

// Icon64 is the default 64x64 application icon.
//
//go:embed icons/icon-64.png
var Icon64 []byte

// Icon256 is the default 256x256 application icon.
//
//go:embed icons/icon-256.png
var Icon256 []byte
