package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestStatEntryAndRoot exercises the platform stat layer through the portable
// surface both implementations share: the walk only ever sees entryInfo.
func TestStatEntryAndRoot(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "x.bin"), 4096)

	isDir, _, err := statRoot(dir)
	if err != nil {
		t.Fatal(err)
	}

	if !isDir {
		t.Error("statRoot on a directory want isDir=true")
	}

	isDir, _, err = statRoot(filepath.Join(dir, "x.bin"))
	if err != nil {
		t.Fatal(err)
	}

	if isDir {
		t.Error("statRoot on a file want isDir=false")
	}

	if _, _, err := statRoot(filepath.Join(dir, "missing")); err == nil {
		t.Error("statRoot on a missing path want an error")
	}

	f, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = f.Close() }()

	info, err := dirCtx{path: dir, fd: dirFD(f)}.statEntry("x.bin")
	if err != nil {
		t.Fatal(err)
	}

	if info.size != 4096 {
		t.Errorf("size = %d, want 4096", info.size)
	}

	if info.disk < info.size {
		t.Errorf("disk = %d, want >= size %d", info.disk, info.size)
	}

	if info.nlink < 1 {
		t.Errorf("nlink = %d, want >= 1", info.nlink)
	}

	if _, err := (dirCtx{path: dir, fd: dirFD(f)}).statEntry("gone.bin"); err == nil {
		t.Error("statEntry on a missing entry want an error")
	}
}

func TestScanRejectsNonDirectoryRoot(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f.bin")
	writeFile(t, file, 16)

	if _, err := Scan(context.Background(), file, nil); err == nil {
		t.Error("Scan with a file root want an error")
	}
}

func TestScanAcceptsTrailingSeparatorRoot(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.bin"), 4096)

	res, err := Scan(context.Background(), root+string(filepath.Separator), nil)
	if err != nil {
		t.Fatal(err)
	}

	if res.Err != nil {
		t.Fatal(res.Err)
	}

	if len(res.Files) != 1 || res.Files[0].Name != "a.bin" {
		t.Errorf("files = %v, want just a.bin", res.Files)
	}
}
