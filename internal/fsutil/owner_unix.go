//go:build unix

package fsutil

import (
	"os"
	"syscall"
)

// keepOwner gives f the uid and gid of prev when they differ from ours and
// we are allowed to change them. Non-root processes cannot chown to another
// user, and in that case the file already belongs to the right one.
func keepOwner(f *os.File, prev os.FileInfo) error {
	uid, gid, ok := ownerToApply(prev)
	if !ok {
		return nil
	}
	return f.Chown(uid, gid)
}

// keepOwnerPath is keepOwner for a path instead of an open file.
func keepOwnerPath(path string, prev os.FileInfo) error {
	uid, gid, ok := ownerToApply(prev)
	if !ok {
		return nil
	}
	return os.Chown(path, uid, gid)
}

func ownerToApply(prev os.FileInfo) (uid, gid int, ok bool) {
	st, isStat := prev.Sys().(*syscall.Stat_t)
	if !isStat {
		return 0, 0, false
	}
	uid, gid = int(st.Uid), int(st.Gid)
	if uid == os.Geteuid() && gid == os.Getegid() {
		return 0, 0, false
	}
	if os.Geteuid() != 0 {
		return 0, 0, false
	}
	return uid, gid, true
}
