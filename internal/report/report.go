// Package report turns flat scan results into a directory tree with size
// rollups and renders them as text or JSON.
package report

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"unicode"
	"unicode/utf8"

	"github.com/jbrusegaard/hoardio/internal/scanner"
)

// Node is one entry (file or directory) in the size tree. Directory sizes are
// the sum of everything below them; file nodes have no children.
type Node struct {
	Name         string  `json:"name"`
	Dir          bool    `json:"dir"`
	DiskUsage    int64   `json:"diskBytes"`
	ApparentSize int64   `json:"apparentBytes"`
	Children     []*Node `json:"children,omitempty"`

	childIdx map[string]*Node `json:"-"`
}

// Tree is the full scan rendered for output.
type Tree struct {
	Root  *Node          `json:"root"`
	Files []scanner.File `json:"-"`
}

const sep = string(filepath.Separator)

// Build assembles the tree for root from flat file entries, rolling sizes up
// into every ancestor directory as files are inserted.
func Build(root string, files []scanner.File) *Tree {
	root = filepath.Clean(root)
	t := &Tree{Root: &Node{Name: root, Dir: true, childIdx: map[string]*Node{}}, Files: files}

	// Scan output is rooted at root by construction, so the relative path is a
	// prefix cut: filepath.Rel costs ~300x more and can never disagree here.
	prefix := root
	if !strings.HasSuffix(prefix, sep) {
		prefix += sep
	}

	for _, f := range files {
		rel, ok := strings.CutPrefix(f.Path, prefix)
		if !ok || rel == "" {
			continue // file outside root (should not happen)
		}

		cur := t.Root
		cur.addSizes(f)

		dir, name := "", rel
		if i := strings.LastIndexByte(rel, filepath.Separator); i >= 0 {
			dir, name = rel[:i], rel[i+1:]
		}

		for rest := dir; rest != ""; {
			var part string

			part, rest, _ = strings.Cut(rest, sep)
			cur = cur.child(part, true)
			cur.addSizes(f)
		}

		leaf := cur.child(name, false)
		leaf.addSizes(f)
	}

	t.Root.pruneIdx()

	return t
}

// child returns the named child, creating it on first use.
func (n *Node) child(name string, dir bool) *Node {
	if c, ok := n.childIdx[name]; ok {
		return c
	}

	c := &Node{Name: name, Dir: dir}
	if dir {
		c.childIdx = map[string]*Node{}
	}

	n.childIdx[name] = c
	n.Children = append(n.Children, c)

	return c
}

func (n *Node) addSizes(f scanner.File) {
	n.DiskUsage += f.DiskUsage
	n.ApparentSize += f.ApparentSize
}

func (n *Node) pruneIdx() {
	n.childIdx = nil
	for _, c := range n.Children {
		c.pruneIdx()
	}
}

// SortBySize orders children by disk usage, largest first, recursively.
func SortBySize(n *Node) {
	slices.SortStableFunc(n.Children, func(a, b *Node) int {
		switch {
		case a.DiskUsage > b.DiskUsage:
			return -1
		case a.DiskUsage < b.DiskUsage:
			return 1
		default:
			return 0
		}
	})

	for _, c := range n.Children {
		if c.Dir {
			SortBySize(c)
		}
	}
}

// SortByName orders children alphabetically (case-insensitive), dirs first,
// recursively.
func SortByName(n *Node) {
	slices.SortStableFunc(n.Children, func(a, b *Node) int {
		if a.Dir != b.Dir {
			if a.Dir {
				return -1
			}

			return 1
		}

		return compareFold(a.Name, b.Name)
	})

	for _, c := range n.Children {
		if c.Dir {
			SortByName(c)
		}
	}
}

// compareFold compares two names case-insensitively without allocating a
// lowered copy of either (strings.ToLower per comparison dominated this sort).
func compareFold(a, b string) int {
	for a != "" && b != "" {
		ra, wa := utf8.DecodeRuneInString(a)
		rb, wb := utf8.DecodeRuneInString(b)

		if la, lb := unicode.ToLower(ra), unicode.ToLower(rb); la != lb {
			if la < lb {
				return -1
			}

			return 1
		}

		a, b = a[wa:], b[wb:]
	}

	return cmp.Compare(len(a), len(b))
}

// fprintf writes formatted output and tags any failure with a section label.
func fprintf(w io.Writer, section, format string, a ...any) error {
	if _, err := fmt.Fprintf(w, format, a...); err != nil {
		return fmt.Errorf("%s: %w", section, err)
	}

	return nil
}

// Options controls Text rendering.
type Options struct {
	TopFiles  int    // number of largest files to list; 0 = default 30
	MinDisk   int64  // hide entries below this many allocated bytes
	TreeDepth int    // directory tree depth to render; 0 = default 2
	Duration  string // wall-clock scan time for the summary line
}

// Text renders the human-readable report: summary, directory tree, largest files.
func Text(w io.Writer, t *Tree, res *scanner.Result, opts Options) error {
	if opts.TopFiles <= 0 {
		opts.TopFiles = 30
	}

	if opts.TreeDepth <= 0 {
		opts.TreeDepth = 2
	}

	SortBySize(t.Root)

	if err := writeSummary(w, t, res, opts); err != nil {
		return err
	}

	if err := fprintf(w, "tree header", "\nDirectory tree (top %d levels, by disk usage)\n", opts.TreeDepth); err != nil {
		return err
	}

	if err := t.Root.renderSub(w, opts.TreeDepth, t.Root.DiskUsage, opts.MinDisk, ""); err != nil {
		return err
	}

	if err := writeTopFiles(w, t, opts); err != nil {
		return err
	}

	return writeUnreadable(w, res)
}

func writeSummary(w io.Writer, t *Tree, res *scanner.Result, opts Options) error {
	if err := fprintf(w, "summary", "Scan of %s\n", t.Root.Name); err != nil {
		return err
	}

	if err := fprintf(w, "summary", "%d dirs scanned, %d files, %s on disk", res.Stats.DirsScanned, res.Stats.FilesSeen, scanner.HumanBytes(res.Stats.BytesDisk)); err != nil {
		return err
	}

	if opts.Duration != "" {
		if err := fprintf(w, "summary", " in %s", opts.Duration); err != nil {
			return err
		}
	}

	if err := fprintf(w, "summary", "\n"); err != nil {
		return err
	}

	if n := len(res.Errors); n > 0 {
		return fprintf(w, "summary", "%d directories could not be read (see --errors or JSON output)\n", n)
	}

	return nil
}

func writeTopFiles(w io.Writer, t *Tree, opts Options) error {
	if err := fprintf(w, "files header", "\nTop %d largest files\n", opts.TopFiles); err != nil {
		return err
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "DISK USAGE\tAPPARENT\tPATH"); err != nil {
		return fmt.Errorf("files header: %w", err)
	}

	for _, f := range topFiles(t.Files, opts.TopFiles, opts.MinDisk) {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\n", scanner.HumanBytes(f.DiskUsage), scanner.HumanBytes(f.ApparentSize), f.Path); err != nil {
			return fmt.Errorf("files row: %w", err)
		}
	}

	if err := tw.Flush(); err != nil {
		return fmt.Errorf("flush top files table: %w", err)
	}

	return nil
}

func writeUnreadable(w io.Writer, res *scanner.Result) error {
	if len(res.Errors) == 0 {
		return nil
	}

	if err := fprintf(w, "unreadable header", "\nUnreadable directories (%d)\n", len(res.Errors)); err != nil {
		return err
	}

	for _, e := range res.Errors {
		if err := fprintf(w, "unreadable entry", "  %s: %v\n", e.Path, e.Err); err != nil {
			return err
		}
	}

	return nil
}

func (n *Node) renderSub(w io.Writer, depth int, total, minDisk int64, indent string) error {
	if depth <= 0 {
		return nil
	}

	for _, c := range n.Children {
		if c.DiskUsage < minDisk {
			continue
		}

		pct := 0.0
		if total > 0 {
			pct = 100 * float64(c.DiskUsage) / float64(total)
		}

		name := c.Name + "/"
		if !c.Dir {
			name = c.Name
		}

		if err := fprintf(w, "tree row", "%8s  %5.1f%%  %s%s\n", scanner.HumanBytes(c.DiskUsage), pct, indent, name); err != nil {
			return err
		}

		if c.Dir {
			if err := c.renderSub(w, depth-1, total, minDisk, indent+"  "); err != nil {
				return err
			}
		}
	}

	return nil
}

// topFiles returns the n largest files, descending, breaking ties in input
// order (matching a stable sort of the whole slice). It keeps a sorted window
// of n instead of copying and sorting every file: the reject test costs one
// comparison for the ~99% of files that cannot make the cut.
func topFiles(files []scanner.File, n int, minDisk int64) []scanner.File {
	var keep []scanner.File

	for _, f := range files {
		if f.DiskUsage < minDisk {
			continue
		}

		full := len(keep) == n
		if full && f.DiskUsage <= keep[n-1].DiskUsage {
			continue // would sort after the current last place
		}

		at := len(keep)
		if full {
			at = n - 1 // the last place is about to fall out of the window
		}

		for at > 0 && keep[at-1].DiskUsage < f.DiskUsage {
			at-- // equals stay ahead: preserves stable ordering
		}

		keep = insertAt(keep, at, f)
		if full {
			keep = keep[:n]
		}
	}

	return keep
}

// insertAt shifts keep[at:] right and places f at at.
func insertAt(keep []scanner.File, at int, f scanner.File) []scanner.File {
	keep = append(keep, scanner.File{})
	copy(keep[at+1:], keep[at:])
	keep[at] = f

	return keep
}

type jsonError struct {
	Path string `json:"path"`
	Err  string `json:"error"`
}

type jsonReport struct {
	Root        string        `json:"root"`
	Stats       scanner.Stats `json:"stats"`
	Interrupted bool          `json:"interrupted,omitempty"`
	Errors      []jsonError   `json:"errors,omitempty"`
	Tree        *Node         `json:"tree"`
}

// JSON renders the full tree and scan metadata as JSON.
func JSON(w io.Writer, t *Tree, res *scanner.Result) error {
	rep := jsonReport{Root: t.Root.Name, Stats: res.Stats, Tree: t.Root}
	if res.Err != nil {
		rep.Interrupted = true
	}

	for _, e := range res.Errors {
		rep.Errors = append(rep.Errors, jsonError{Path: e.Path, Err: e.Err.Error()})
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	if err := enc.Encode(rep); err != nil {
		return fmt.Errorf("encode json report: %w", err)
	}

	return nil
}
