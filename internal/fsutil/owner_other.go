//go:build !unix

package fsutil

import "os"

// keepOwner is a no-op where files do not carry a uid and gid.
func keepOwner(*os.File, os.FileInfo) error { return nil }

func keepOwnerPath(string, os.FileInfo) error { return nil }
