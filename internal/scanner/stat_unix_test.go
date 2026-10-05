//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package scanner

import (
	"testing"

	"golang.org/x/sys/unix"
)

// blockBytes reports a path's on-disk allocation as the unix stat reports it:
// st_blocks * 512.
func blockBytes(t *testing.T, path string) int64 {
	t.Helper()

	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		t.Fatal(err)
	}

	return st.Blocks * 512
}
