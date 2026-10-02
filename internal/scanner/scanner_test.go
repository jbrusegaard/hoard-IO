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

func blockBytes(t *testing.T, path string) int64 {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return diskUsageOf(st)
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

func TestHardlinkCountedOnce(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src.bin")
	writeFile(t, src, 4096)
	if err := os.Link(src, filepath.Join(root, "dup.bin")); err != nil {
		t.Skipf("hardlinks unsupported: %v", err)
	}

	res, err := Scan(context.Background(), root, &Options{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, f := range res.Files {
		if filepath.Base(f.Path) == "src.bin" || filepath.Base(f.Path) == "dup.bin" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("hard-linked file counted %d times, want 1", count)
	}
}

func TestSymlinkNotFollowed(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "target.bin"), 4096)

	// symlink to a file and a symlink loop back into the tree
	if err := os.Symlink(filepath.Join(outside, "target.bin"), filepath.Join(root, "link-file")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(root, "loop")); err != nil {
		t.Fatal(err)
	}

	res, err := Scan(context.Background(), root, &Options{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Files {
		if filepath.Base(f.Path) == "target.bin" || filepath.Base(f.Path) == "link-file" {
			t.Errorf("symlink target %s should not be scanned", f.Path)
		}
	}
	if res.Stats.DirsScanned != 1 {
		t.Errorf("DirsScanned = %d, want 1 (symlink loop must not recurse)", res.Stats.DirsScanned)
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

func TestUnreadableDirRecordedNotFatal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; permissions not enforced")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "ok.bin"), 4096)
	locked := filepath.Join(root, "locked", "hidden.bin")
	writeFile(t, locked, 4096)
	if err := os.Chmod(filepath.Dir(locked), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Dir(locked), 0o755) })

	res, err := Scan(context.Background(), root, &Options{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Err != nil {
		t.Fatalf("scan should continue past unreadable dir: %v", res.Err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("got %d errors, want 1", len(res.Errors))
	}
	if filepath.Base(res.Errors[0].Path) != "locked" {
		t.Errorf("error recorded for %s, want locked dir", res.Errors[0].Path)
	}
	if !paths(res.Files)["ok.bin"] {
		t.Error("scan should still collect readable files")
	}
}

func TestWideFanOutCompletes(t *testing.T) {
	// Regression: with a bounded dirs channel and few workers, wide trees
	// deadlock (every worker blocks on send, nobody receives).
	root := t.TempDir()
	total := 0
	for i := 0; i < 6; i++ {
		for j := 0; j < 6; j++ {
			for k := 0; k < 6; k++ {
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
	for i := 0; i < 50; i++ { // enough dirs that a cancelled scan is meaningful
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
