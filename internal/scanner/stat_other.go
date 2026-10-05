//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package scanner

import (
	"fmt"
	"os"
)

// supportsDev marks platforms whose stats carry no device id or inode (Windows,
// js, plan9 and friends): -xdev is skipped and hardlink dedupe cannot run.
const supportsDev = false

// dirFD returns noFD: the portable fallback stats by path, so it never needs
// the descriptor, and taking it would only disable the file's finalizer.
func dirFD(*os.File) int { return noFD }

// noFD marks "no directory descriptor available" in dirCtx.
const noFD = -1

// statEntry stats one entry by full path. Windows has no fstatat equivalent
// reachable from a directory handle here, and its FileInfo exposes neither an
// inode nor a link count nor an allocated size, so disk usage falls back to the
// apparent size and hardlink dedupe is inert (zero inode).
func (d dirCtx) statEntry(name string) (entryInfo, error) {
	info, err := os.Lstat(joinPath(d.path, name))
	if err != nil {
		return entryInfo{}, fmt.Errorf("lstat %s: %w", name, err)
	}

	size := info.Size()

	return entryInfo{nlink: 1, disk: size, size: size}, nil
}

// statRoot stats the scan root. Device identity is unavailable, so it reports
// the same zero device every entry reports, which makes -xdev a no-op.
func statRoot(path string) (isDir bool, dev int64, err error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, 0, fmt.Errorf("stat %s: %w", path, err)
	}

	return info.IsDir(), 0, nil
}
