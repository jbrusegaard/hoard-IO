// Command hoardio inventories disk usage under a directory tree. It is
// strictly read-only: it never creates, modifies, moves, or deletes anything.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jbrusegaard/hoardio/internal/report"
	"github.com/jbrusegaard/hoardio/internal/scanner"
	"github.com/jbrusegaard/hoardio/internal/tui"
)

var errUsage = errors.New("usage error")

// runTUI scans in a background goroutine and drives the interactive browser.
// in/out are injectable for tests; nil means use the terminal.
func runTUI(root string, opts *scanner.Options, in io.Reader, out io.Writer) error {
	m := tui.New()
	m.SetScanRoot(root)

	progOpts := []tea.ProgramOption{tea.WithAltScreen()}
	if in != nil {
		progOpts = append(progOpts, tea.WithInput(in))
	}

	if out != nil {
		progOpts = append(progOpts, tea.WithOutput(out))
	}

	p := tea.NewProgram(m, progOpts...)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scanStart := time.Now()

	scanOpts := opts
	scanOpts.OnProgress = func(s scanner.Stats) {
		p.Send(tui.Progress(s, time.Since(scanStart)))
	}

	go func() {
		res, err := scanner.Scan(ctx, root, scanOpts)
		if err != nil {
			p.Send(tui.ScanFailed(err))
			return
		}

		tree := report.Build(res.Root, res.Files)
		p.Send(tui.ScanDone(tree, res.Stats, time.Since(scanStart), res.Err != nil))
	}()

	_, err := p.Run()

	cancel() // stop the scan if the user quit mid-scan

	if err != nil {
		return fmt.Errorf("tui: %w", err)
	}

	return nil
}

// version is injected at build time via:
// go build -ldflags "-X main.version=$(git describe --tags --always)".
var version = "dev"

type excludedirs []string

func (e *excludedirs) String() string { return strings.Join(*e, ",") }

func (e *excludedirs) Set(v string) error {
	*e = append(*e, v)
	return nil
}

const usageExit = 2

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "hoardio:", err)
		os.Exit(exitCode(err))
	}
}

func exitCode(err error) int {
	if errors.Is(err, errUsage) {
		return usageExit
	}

	return 1
}

// textFlags carries the plain-report flags from run to runText.
type textFlags struct {
	top        int
	minDisk    int64
	depth      int
	json       bool
	noProgress bool
	excludes   []string
}

func run() error {
	var (
		excludes   excludedirs
		top        = flag.Int("top", 30, "number of largest files to list")
		minSize    = flag.String("min-size", "0", "hide entries below this size (e.g. 10M, 1.5G)")
		xdev       = flag.Bool("xdev", false, "stay on one filesystem (skip other st_dev)")
		jsonOut    = flag.Bool("json", false, "emit the full tree as JSON")
		workers    = flag.Int("workers", 0, "scan worker goroutines (default 2x CPU cores)")
		depth      = flag.Int("tree-depth", 2, "directory tree levels to render")
		noProgress = flag.Bool("no-progress", false, "disable the progress line (text mode)")
		textOut    = flag.Bool("text", false, "force the plain text report instead of the interactive TUI")
		showVer    = flag.Bool("version", false, "print version and exit")
	)

	flag.Usage = usage

	flag.Var(&excludes, "exclude", "glob pattern to skip (base name or full path); repeatable")
	flag.Parse()

	if *showVer {
		fmt.Println(version)
		return nil
	}

	minDisk, err := parseSize(*minSize)
	if err != nil {
		return fmt.Errorf("--min-size: %w", err)
	}

	root, err := resolveRoot(flag.Arg(0), flag.Args())
	if err != nil {
		return err
	}

	scanOpts := &scanner.Options{Workers: *workers, XDev: *xdev, Excludes: excludes}

	useTUI := !*jsonOut && !*textOut && isTerminal(os.Stdin) && isTerminal(os.Stdout)
	if useTUI {
		if err := runTUI(root, scanOpts, nil, nil); err != nil {
			return fmt.Errorf("tui: %w", err)
		}

		return nil
	}

	tf := textFlags{top: *top, minDisk: minDisk, depth: *depth, json: *jsonOut, noProgress: *noProgress, excludes: excludes}

	return runText(root, scanOpts, tf)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: hoardio [flags] [path]")
	fmt.Fprintln(os.Stderr, "\nFind space hogs under a directory tree. Strictly read-only:")
	fmt.Fprintln(os.Stderr, "this tool never deletes, moves, or modifies anything.")
	fmt.Fprintln(os.Stderr, "\ndefault path: $HOME")
	flag.PrintDefaults()
}

// resolveRoot applies the default path and absolutizes the single positional
// argument (at most one is accepted).
func resolveRoot(arg string, allArgs []string) (string, error) {
	root := arg
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("no path given and $HOME unavailable: %w", err)
		}

		root = home
	}

	if n := len(allArgs); n > 1 {
		return "", fmt.Errorf("%w: expected at most one path, got %d", errUsage, n)
	}

	root, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", root, err)
	}

	return root, nil
}

// runText scans and renders the plain text or JSON report.
func runText(root string, scanOpts *scanner.Options, tf textFlags) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if !tf.noProgress && !tf.json && isTerminal(os.Stderr) {
		scanOpts.Progress = os.Stderr
	}

	start := time.Now()

	res, err := scanner.Scan(ctx, root, scanOpts)
	if err != nil {
		return fmt.Errorf("scan %s: %w", root, err)
	}

	elapsed := time.Since(start)

	return renderReport(os.Stdout, os.Stderr, res, tf, elapsed)
}

// renderReport writes warnings, then the text or JSON report to w; warn
// receives operator notes (bad patterns, interruption).
func renderReport(w, warn io.Writer, res *scanner.Result, tf textFlags, elapsed time.Duration) error {
	for _, p := range scanner.BadPatterns(tf.excludes) {
		// Operator warning; a failed note must not abort the report.
		_, _ = fmt.Fprintf(warn, "warning: invalid --exclude pattern %q ignored\n", p)
	}

	tree := report.Build(res.Root, res.Files)

	if tf.json {
		if err := report.JSON(w, tree, res); err != nil {
			return fmt.Errorf("write json: %w", err)
		}

		return nil
	}

	opts := report.Options{TopFiles: tf.top, MinDisk: tf.minDisk, TreeDepth: tf.depth, Duration: elapsed.Round(10 * time.Millisecond).String()}
	if err := report.Text(w, tree, res, opts); err != nil {
		return fmt.Errorf("write report: %w", err)
	}

	if res.Err != nil {
		// Same as above: the report itself already succeeded.
		_, _ = fmt.Fprintf(warn, "scan interrupted; results are partial (%v)\n", res.Err)
	}

	return nil
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice != 0
}

var sizeUnits = map[string]int64{"K": 1 << 10, "M": 1 << 20, "G": 1 << 30, "T": 1 << 40}

// parseSize accepts "512", "10M", "1.5G" (binary units).
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}

	var mult int64 = 1
	if u, ok := sizeUnits[strings.ToUpper(s[len(s)-1:])]; ok {
		mult = u
		s = strings.TrimSpace(s[:len(s)-1])
	}

	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("invalid size %q", s)
	}

	return int64(f * float64(mult)), nil
}
