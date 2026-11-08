package report

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
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

// TestBuildKeepsDotDotPrefixedNames checks that only the exact parent
// prefixes ".." and "../" drop entries, so real names starting with two
// dots stay in the tree with their sizes rolled up.
func TestBuildKeepsDotDotPrefixedNames(t *testing.T) {
	root := "/r"
	const weird = "..weird"
	const weirdSize = 300 << 10
	wantTotal := int64((300 + 52 + 100) << 10)

	tr := Build(root, []scanner.File{
		mkFile(root, filepath.Join(weird, "big.bin"), weirdSize, weirdSize),
		mkFile(root, "..dots.bin", 52<<10, 52<<10),
		mkFile(root, filepath.Join("normal", "ok.bin"), 100<<10, 100<<10),
	})

	if tr.Root.DiskUsage != wantTotal {
		t.Errorf("root rollup = %d, want %d", tr.Root.DiskUsage, wantTotal)
	}

	dir := find(t, tr.Root, weird)
	if !dir.Dir || dir.DiskUsage != weirdSize {
		t.Errorf("%s rollup = %d (dir=%v), want %d/true", weird, dir.DiskUsage, dir.Dir, weirdSize)
	}

	if leaf := find(t, dir, "big.bin"); leaf.Dir || leaf.DiskUsage != weirdSize {
		t.Errorf("big.bin node wrong: dir=%v size=%d", leaf.Dir, leaf.DiskUsage)
	}

	dots := find(t, tr.Root, "..dots.bin")
	if dots.Dir || dots.DiskUsage != 52<<10 {
		t.Errorf("..dots.bin node wrong: dir=%v size=%d", dots.Dir, dots.DiskUsage)
	}

	res := &scanner.Result{Root: root, Stats: scanner.Stats{DirsScanned: 2, FilesSeen: 3, BytesDisk: wantTotal}}

	var buf bytes.Buffer
	if err := Text(&buf, tr, res, Options{TopFiles: 5, TreeDepth: 2}); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.Contains(out, weird+"/") {
		t.Errorf("tree must list %s/:\n%s", weird, out)
	}

	if strings.Contains(out, "100.0%") {
		t.Errorf("no child holds the whole scan, so no row should read 100.0%%:\n%s", out)
	}
}

// TestBuildSkipsPathAboveRoot keeps genuinely parent-relative paths out of
// the tree now that the skip test only rejects "../".
func TestBuildSkipsPathAboveRoot(t *testing.T) {
	root := "/r/sub"
	tr := Build(root, []scanner.File{
		{Path: root + "/keep.bin", Name: "keep.bin", DiskUsage: 7, ApparentSize: 7},
		{Path: "/r/escape.bin", Name: "escape.bin", DiskUsage: 99, ApparentSize: 99},
	})

	if tr.Root.DiskUsage != 7 {
		t.Errorf("root rollup = %d, want 7", tr.Root.DiskUsage)
	}

	for _, c := range tr.Root.Children {
		if c.Name == "escape.bin" {
			t.Fatalf("file above root entered the tree: %v", names(tr.Root.Children))
		}
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

// TestTextUnreadableSummaryLine checks the summary count line pluralizes and
// no longer points at a --errors flag that does not exist. The full list must
// still print under the unreadable heading.
func TestTextUnreadableSummaryLine(t *testing.T) {
	root := "/r"

	cases := []struct {
		name string
		errs []scanner.DirError
		want string
	}{
		{
			name: "one",
			errs: []scanner.DirError{{Path: root + "/nope", Err: errWriterDead}},
			want: "1 directory could not be read\n",
		},
		{
			name: "many",
			errs: []scanner.DirError{
				{Path: root + "/a", Err: errWriterDead},
				{Path: root + "/b", Err: errWriterDead},
			},
			want: "2 directories could not be read\n",
		},
	}

	for _, tc := range cases {
		tr := Build(root, []scanner.File{mkFile(root, "a.bin", 10, 10)})
		res := &scanner.Result{Root: root, Stats: scanner.Stats{FilesSeen: 1, BytesDisk: 10}, Errors: tc.errs}

		var buf bytes.Buffer
		if err := Text(&buf, tr, res, Options{TopFiles: 1, TreeDepth: 1}); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}

		out := buf.String()
		if !strings.Contains(out, tc.want) {
			t.Errorf("%s: summary missing %q:\n%s", tc.name, tc.want, out)
		}

		if strings.Contains(out, "--errors") {
			t.Errorf("%s: summary must not mention --errors:\n%s", tc.name, out)
		}

		for _, e := range tc.errs {
			if !strings.Contains(out, e.Path) {
				t.Errorf("%s: unreadable list missing %s:\n%s", tc.name, e.Path, out)
			}
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
