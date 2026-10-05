//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package scanner

import (
	"os"
	"testing"
)

// blockBytes reports a path's on-disk allocation. The portable fallback has no
// allocation info, so it reports the apparent size - exactly what the scan
// itself records on these platforms.
func blockBytes(t *testing.T, path string) int64 {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	return info.Size()
}
