//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package scanner

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// supportsDev marks platforms where stats carry a device id and an inode:
// -xdev and hardlink dedupe only mean something there.
const supportsDev = true

// dirFD hands the walk the raw descriptor behind an open directory. The walk
// owns the file and closes it explicitly, so disabling the finalizer (which
// Fd does) costs nothing.
func dirFD(f *os.File) int { return int(f.Fd()) }

// statEntry stats one entry relative to the directory fd the walk already
// holds. Fstatat resolves a single path component instead of the whole path and
// needs no FileInfo wrapper, which makes it roughly a quarter cheaper than
// os.Lstat on the joined path - or DirEntry.Info, which joins the path itself.
func (d dirCtx) statEntry(name string) (entryInfo, error) {
	var st unix.Stat_t

	if err := unix.Fstatat(d.fd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return entryInfo{}, fmt.Errorf("fstatat %s: %w", name, err)
	}

	return entryInfo{
		dev:   int64(st.Dev),
		ino:   st.Ino,
		nlink: int64(st.Nlink),
		disk:  st.Blocks * 512,
		size:  st.Size,
	}, nil
}

// statRoot stats the scan root: is it a directory, and on which device.
func statRoot(path string) (isDir bool, dev int64, err error) {
	var st unix.Stat_t

	if err := unix.Lstat(path, &st); err != nil {
		return false, 0, fmt.Errorf("stat %s: %w", path, err)
	}

	return st.Mode&unix.S_IFMT == unix.S_IFDIR, int64(st.Dev), nil
}
