package report

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/jbrusegaard/hoardio/internal/scanner"
)

var errWriterDead = errors.New("writer dead")

const (
	mntRoot = "/mnt"
	bigName = "big.bin"
)

// failWriter accepts n successful writes, then fails every subsequent write.
type failWriter struct {
	left int
}

func (f *failWriter) Write(p []byte) (int, error) {
	if f.left <= 0 {
		return 0, errWriterDead
	}

	f.left--

	return len(p), nil
}

// countWrites returns how many successful Write calls Text makes.
func countWrites(t *testing.T, tr *Tree, res *scanner.Result, opts Options) int {
	t.Helper()
	counter := &countingWriter{}
	if err := Text(counter, tr, res, opts); err != nil {
		t.Fatalf("Text to counting writer: %v", err)
	}

	return counter.writes
}

type countingWriter struct{ writes int }

func (c *countingWriter) Write(p []byte) (int, error) {
	c.writes++

	return len(p), nil
}

// TestTextFailsAtEveryWriteStage drives a writer that dies after k writes and
// requires Text to surface an error for every k below the total write count.
func TestTextFailsAtEveryWriteStage(t *testing.T) {
	root := mntRoot
	files := []scanner.File{
		{Path: root + "/dir/" + bigName, Name: bigName, DiskUsage: 900, ApparentSize: 900},
		{Path: root + "/tiny.txt", Name: "tiny.txt", DiskUsage: 10, ApparentSize: 10},
	}
	tr := Build(root, files)
	res := &scanner.Result{Root: root, Stats: scanner.Stats{DirsScanned: 2, FilesSeen: 2, BytesDisk: 910}, Errors: []scanner.DirError{{Path: root + "/nope", Err: errWriterDead}}}

	total := countWrites(t, tr, res, Options{TopFiles: 2, TreeDepth: 2})
	if total < 4 {
		t.Fatalf("expected several writes, got %d", total)
	}

	for k := range total {
		err := Text(&failWriter{left: k}, tr, res, Options{TopFiles: 2, TreeDepth: 2})
		if err == nil {
			t.Errorf("Text with writer failing after %d writes: expected error", k)
		} else if !errors.Is(err, errWriterDead) {
			t.Errorf("Text fail-after-%d: want wrapped %v, got %v", k, errWriterDead, err)
		}
	}

	if err := Text(&failWriter{left: total}, tr, res, Options{TopFiles: 2, TreeDepth: 2}); err != nil {
		t.Errorf("Text with healthy writer failed: %v", err)
	}
}

func TestJSONFailsOnDeadWriter(t *testing.T) {
	tr := Build(mntRoot, []scanner.File{{Path: mntRoot + "/a", Name: "a", DiskUsage: 1}})
	res := &scanner.Result{Root: mntRoot}
	if err := JSON(&failWriter{}, tr, res); !errors.Is(err, errWriterDead) {
		t.Errorf("JSON on dead writer: want %v wrapped, got %v", errWriterDead, err)
	}
}

func TestJSONInterruptedAndErrors(t *testing.T) {
	tr := Build(mntRoot, []scanner.File{{Path: mntRoot + "/a", Name: "a", DiskUsage: 1}})
	res := &scanner.Result{
		Root:   "/mnt",
		Err:    errors.New("context canceled"),
		Errors: []scanner.DirError{{Path: "/mnt/x", Err: errors.New("permission denied")}},
	}
	var buf bytes.Buffer
	if err := JSON(&buf, tr, res); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	for _, want := range []string{`"interrupted": true`, `"path": "/mnt/x"`, "permission denied"} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON missing %s", want)
		}
	}
}

func TestTextZeroTotalNoPercentDivide(t *testing.T) {
	root := "/zero"
	tr := Build(root, nil)
	tr.Root.Children = []*Node{{Name: "ghost", Dir: true, DiskUsage: 0}}
	var buf bytes.Buffer
	if err := Text(&buf, tr, &scanner.Result{Root: root}, Options{TopFiles: 1, TreeDepth: 1}); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(buf.String(), " 0.0%") {
		t.Errorf("zero-total tree should render 0.0%%, got:\n%s", buf.String())
	}
}

func TestRenderSubMinDiskBoundary(t *testing.T) {
	root := "/b"
	tr := Build(root, []scanner.File{
		{Path: root + "/exact", Name: "exact", DiskUsage: 100, ApparentSize: 100},
		{Path: root + "/under", Name: "under", DiskUsage: 99, ApparentSize: 99},
	})
	var buf bytes.Buffer
	if err := Text(&buf, tr, &scanner.Result{Root: root}, Options{TopFiles: 5, TreeDepth: 1, MinDisk: 100}); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.Contains(out, "exact") {
		t.Errorf("entry exactly at MinDisk must be shown:\n%s", out)
	}

	if strings.Contains(out, "under") {
		t.Errorf("entry below MinDisk must be hidden:\n%s", out)
	}
}

func TestTopFilesBoundaries(t *testing.T) {
	files := []scanner.File{
		{Path: "/t/a", Name: "a", DiskUsage: 30},
		{Path: "/t/b", Name: "b", DiskUsage: 20},
		{Path: "/t/c", Name: "c", DiskUsage: 10},
	}

	if got := topFiles(files, 3, 0); len(got) != 3 {
		t.Errorf("n == len: want all 3, got %d", len(got))
	}

	if got := topFiles(files, 2, 0); len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Errorf("n < len: want [a b], got %v", got)
	}

	if got := topFiles(files, 5, 20); len(got) != 2 {
		t.Errorf("minDisk == usage: want 2 kept (>=), got %d", len(got))
	}

	if got := topFiles(files, 5, 31); len(got) != 0 {
		t.Errorf("minDisk above all: want none, got %v", got)
	}
}

func TestSortByNameDirsFirstCaseInsensitive(t *testing.T) {
	n := &Node{Name: "root", Dir: true, Children: []*Node{
		{Name: "beta.txt"},
		{Name: "Alpha", Dir: true},
		{Name: "gamma", Dir: true},
		{Name: "delta.txt"},
	}}

	SortByName(n)

	want := []string{"Alpha", "gamma", "beta.txt", "delta.txt"}

	for i, w := range want {
		if n.Children[i].Name != w {
			t.Fatalf("SortByName order %d: want %v, got %v", i, want, names(n.Children))
		}
	}
}

func TestSortBySizeRecursive(t *testing.T) {
	child := &Node{Name: "sub", Dir: true, DiskUsage: 10, Children: []*Node{
		{Name: "small", DiskUsage: 1},
		{Name: "big", DiskUsage: 9},
	}}
	n := &Node{Name: "root", Dir: true, Children: []*Node{
		{Name: "mid", DiskUsage: 5},
		child,
	}}

	SortBySize(n)

	if n.Children[0].Name != "sub" || n.Children[1].Name != "mid" {
		t.Fatalf("top level order wrong: %v", names(n.Children))
	}

	if child.Children[0].Name != "big" {
		t.Fatalf("nested sort failed: %v", names(child.Children))
	}
}

func names(ns []*Node) []string {
	out := make([]string, len(ns))
	for i, x := range ns {
		out[i] = x.Name
	}

	return out
}
