//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// These tests rely on unix filesystem semantics: hard links, symlinks and
// permission bits. Platforms on the portable stat fallback (see stat_other.go)
// have no inodes to dedupe on and treat chmod as a no-op, so they are skipped
// there.

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
