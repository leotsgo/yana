//go:build tools

// Keep the bind package in go.mod at the version the Makefile pins for
// gomobile, so gobind can load it while generating the AAR's bindings.
package crdt

import _ "golang.org/x/mobile/bind"
