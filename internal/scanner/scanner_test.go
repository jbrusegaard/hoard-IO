package scanner

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil { // random so APFS does not compress/sparse
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func paths(files []File) map[string]bool {
	m := make(map[string]bool, len(files))
	for _, f := range files {
		m[filepath.Base(f.Path)] = true
	}
	return m
}

func TestScanCollectsFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.bin"), 4096)
	writeFile(t, filepath.Join(root, "sub", "b.bin"), 8192)
	writeFile(t, filepath.Join(root, "sub", "deep", "c.bin"), 16384)

	res, err := Scan(context.Background(), root, &Options{Workers: 4})
	if err != nil {
		t.Fatal(err)
	}
	if res.Err != nil {
		t.Fatalf("scan error: %v", res.Err)
	}
	got := paths(res.Files)
	for _, name := range []string{"a.bin", "b.bin", "c.bin"} {
		if !got[name] {
			t.Errorf("missing file %s in results", name)
		}
	}
	if res.Stats.DirsScanned != 3 {
		t.Errorf("DirsScanned = %d, want 3", res.Stats.DirsScanned)
	}

	want := map[string]int64{
		filepath.Join(root, "a.bin"):                blockBytes(t, filepath.Join(root, "a.bin")),
		filepath.Join(root, "sub", "b.bin"):         blockBytes(t, filepath.Join(root, "sub", "b.bin")),
		filepath.Join(root, "sub", "deep", "c.bin"): blockBytes(t, filepath.Join(root, "sub", "deep", "c.bin")),
	}
	for _, f := range res.Files {
		if w, ok := want[f.Path]; ok && f.DiskUsage != w {
			t.Errorf("%s DiskUsage = %d, want %d", f.Path, f.DiskUsage, w)
		} else if !ok {
			t.Errorf("unexpected path %s", f.Path)
		}
	}
}

func TestExcludes(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "keep.bin"), 4096)
	writeFile(t, filepath.Join(root, "node_modules", "x.js"), 4096)
	writeFile(t, filepath.Join(root, "vendor", "lib", "y.a"), 4096)

	res, err := Scan(context.Background(), root, &Options{
		Workers:  2,
		Excludes: []string{"node_modules", "vendor"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Files {
		base := filepath.Base(f.Path)
		if base == "x.js" || base == "y.a" {
			t.Errorf("excluded file %s present in results", f.Path)
		}
	}
	if !paths(res.Files)["keep.bin"] {
		t.Error("keep.bin missing")
	}
	if res.Stats.Skipped != 2 {
		t.Errorf("Skipped = %d, want 2", res.Stats.Skipped)
	}
}

func TestExcludeFilePatterns(t *testing.T) {
	// Excludes must apply to files too, not only to directories: a user
	// passing --exclude '*.log' expects log files gone from results.
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "keep.bin"), 4096)
	writeFile(t, filepath.Join(root, "big.log"), 65536)
	writeFile(t, filepath.Join(root, "nested", "cache.dat"), 8192)

	res, err := Scan(context.Background(), root, &Options{
		Workers:  2,
		Excludes: []string{"*.log", "cache.dat"},
	})
	if err != nil {
		t.Fatal(err)
	}

	got := paths(res.Files)
	if got["big.log"] || got["cache.dat"] {
		t.Errorf("excluded file patterns present: %v", got)
	}
	if !got["keep.bin"] {
		t.Error("keep.bin missing")
	}
	if res.Stats.Skipped != 2 {
		t.Errorf("Skipped = %d, want 2", res.Stats.Skipped)
	}
	if res.Stats.FilesSeen != 1 {
		t.Errorf("FilesSeen = %d, want 1", res.Stats.FilesSeen)
	}
}

func TestWideFanOutCompletes(t *testing.T) {
	// Regression: with a bounded dirs channel and few workers, wide trees
	// deadlock (every worker blocks on send, nobody receives).
	root := t.TempDir()
	total := 0
	for i := range 6 {
		for j := range 6 {
			for k := range 6 {
				dir := filepath.Join(root, fmt.Sprintf("a%d", i), fmt.Sprintf("b%d", j), fmt.Sprintf("c%d", k))
				writeFile(t, filepath.Join(dir, "f.bin"), 512)
				total += 3 // a, b, c dirs per leaf chain below root level counted separately
			}
		}
	}
	res, err := Scan(context.Background(), root, &Options{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	want := 1 + 6 + 36 + 216 // root + level counts
	if res.Stats.DirsScanned != int64(want) {
		t.Errorf("DirsScanned = %d, want %d", res.Stats.DirsScanned, want)
	}
	if total == 0 {
		t.Error("fixture empty")
	}
}

func TestCancelStopsScan(t *testing.T) {
	root := t.TempDir()
	for i := range 50 { // enough dirs that a cancelled scan is meaningful
		name := fmt.Sprintf("d%02d", i)
		writeFile(t, filepath.Join(root, name, "f.bin"), 512)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, err := Scan(ctx, root, &Options{Workers: 4})
	if err != nil {
		t.Fatal(err)
	}
	if res.Err == nil {
		t.Error("cancelled scan should report Err")
	}
}
