// Package report turns flat scan results into a directory tree with size
// rollups and renders them as text or JSON.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

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

// Build assembles the tree for root from flat file entries, rolling sizes up
// into every ancestor directory as files are inserted.
func Build(root string, files []scanner.File) *Tree {
	root = filepath.Clean(root)
	t := &Tree{Root: &Node{Name: root, Dir: true, childIdx: map[string]*Node{}}, Files: files}

	for _, f := range files {
		rel, err := filepath.Rel(root, f.Path)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue // file outside root (should not happen)
		}

		cur := t.Root
		cur.DiskUsage += f.DiskUsage
		cur.ApparentSize += f.ApparentSize

		parts := strings.Split(rel, string(filepath.Separator))
		for i, part := range parts {
			leaf := i == len(parts)-1

			child, ok := cur.childIdx[part]
			if !ok {
				child = &Node{Name: part, Dir: !leaf}
				if child.Dir {
					child.childIdx = map[string]*Node{}
				}

				cur.childIdx[part] = child
				cur.Children = append(cur.Children, child)
			}

			child.DiskUsage += f.DiskUsage
			child.ApparentSize += f.ApparentSize
			cur = child
		}
	}

	t.Root.pruneIdx()

	return t
}

func (n *Node) pruneIdx() {
	n.childIdx = nil
	for _, c := range n.Children {
		c.pruneIdx()
	}
}

// SortBySize orders children by disk usage, largest first, recursively.
func SortBySize(n *Node) {
	sort.SliceStable(n.Children, func(i, j int) bool {
		return n.Children[i].DiskUsage > n.Children[j].DiskUsage
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
	sort.SliceStable(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.Dir != b.Dir {
			return a.Dir
		}

		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})

	for _, c := range n.Children {
		if c.Dir {
			SortByName(c)
		}
	}
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

func topFiles(files []scanner.File, n int, minDisk int64) []scanner.File {
	var keep []scanner.File

	for _, f := range files {
		if f.DiskUsage >= minDisk {
			keep = append(keep, f)
		}
	}

	sort.SliceStable(keep, func(i, j int) bool { return keep[i].DiskUsage > keep[j].DiskUsage })

	if len(keep) > n {
		keep = keep[:n]
	}

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
