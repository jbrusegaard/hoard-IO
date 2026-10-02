package report

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/jbrusegaard/hoardio/internal/scanner"
)

func mkFile(root, rel string, disk, apparent int64) scanner.File {
	return scanner.File{
		Path:         filepath.Join(root, rel),
		Name:         filepath.Base(rel),
		DiskUsage:    disk,
		ApparentSize: apparent,
	}
}

func find(t *testing.T, n *Node, name string) *Node {
	t.Helper()
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("child %q not found under %q", name, n.Name)
	return nil
}

func TestBuildRollup(t *testing.T) {
	root := "/data"
	files := []scanner.File{
		mkFile(root, "a.bin", 100, 90),
		mkFile(root, "sub/b.bin", 200, 180),
		mkFile(root, "sub/deep/c.bin", 300, 250),
	}

	tr := Build(root, files)

	if tr.Root.DiskUsage != 600 || tr.Root.ApparentSize != 520 {
		t.Errorf("root rollup = %d/%d, want 600/520", tr.Root.DiskUsage, tr.Root.ApparentSize)
	}
	sub := find(t, tr.Root, "sub")
	if sub.DiskUsage != 500 || !sub.Dir {
		t.Errorf("sub rollup = %d (dir=%v), want 500/true", sub.DiskUsage, sub.Dir)
	}
	deep := find(t, sub, "deep")
	if deep.DiskUsage != 300 {
		t.Errorf("deep rollup = %d, want 300", deep.DiskUsage)
	}
	leaf := find(t, deep, "c.bin")
	if leaf.Dir || leaf.DiskUsage != 300 || len(leaf.Children) != 0 {
		t.Errorf("file node wrong: dir=%v size=%d children=%d", leaf.Dir, leaf.DiskUsage, len(leaf.Children))
	}
}

func TestTopFilesSortAndFilter(t *testing.T) {
	files := []scanner.File{
		mkFile("/r", "small.bin", 10, 10),
		mkFile("/r", bigName, 1000, 999),
		mkFile("/r", "mid.bin", 500, 500),
	}
	top := topFiles(files, 2, 100)
	if len(top) != 2 {
		t.Fatalf("got %d files, want 2", len(top))
	}
	if top[0].Name != bigName || top[1].Name != "mid.bin" {
		t.Errorf("order = %s, %s", top[0].Name, top[1].Name)
	}
}

func TestTextOutputSections(t *testing.T) {
	root := "/r"
	tr := Build(root, []scanner.File{mkFile(root, filepath.Join("sub", bigName), 5000, 4096)})
	res := &scanner.Result{Root: root, Stats: scanner.Stats{DirsScanned: 2, FilesSeen: 1, BytesDisk: 5000}}

	var buf bytes.Buffer
	if err := Text(&buf, tr, res, Options{TopFiles: 5, TreeDepth: 2}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Directory tree", bigName, "sub/", "DISK USAGE"} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestJSONRoundTrip(t *testing.T) {
	root := "/r"
	tr := Build(root, []scanner.File{mkFile(root, "a.bin", 100, 90)})
	res := &scanner.Result{Root: root, Stats: scanner.Stats{FilesSeen: 1, BytesDisk: 100}}

	var buf bytes.Buffer
	if err := JSON(&buf, tr, res); err != nil {
		t.Fatal(err)
	}
	var decoded jsonReport
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if decoded.Tree.DiskUsage != 100 || decoded.Stats.FilesSeen != 1 {
		t.Errorf("decoded tree/stats mismatch: %+v %+v", decoded.Tree, decoded.Stats)
	}
}
