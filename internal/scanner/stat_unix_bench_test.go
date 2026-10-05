//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkFstatatReuse is the scanner's real per-entry path: one fstatat
// relative to the directory fd the walk already holds. It resolves a single
// path component instead of the whole path and needs no FileInfo wrapper, which
// is why it beats BenchmarkEntryInfo on the same directory.
func BenchmarkFstatatReuse(b *testing.B) {
	root := setupFixture(b)
	dir := filepath.Join(root, "d00", "s00")
	b.ReportAllocs()

	for b.Loop() {
		f, err := os.Open(dir)
		if err != nil {
			b.Fatal(err)
		}

		entries, err := f.ReadDir(-1)
		if err != nil {
			b.Fatal(err)
		}

		var sum int64

		d := dirCtx{path: dir, fd: dirFD(f)}

		for _, e := range entries {
			info, err := d.statEntry(e.Name())
			if err != nil {
				b.Fatal(err)
			}

			sum += info.size
		}

		if err := f.Close(); err != nil {
			b.Fatal(err)
		}

		if sum == 0 {
			b.Fatal("no sizes")
		}
	}
}
