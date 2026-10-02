// Package scanner walks a directory tree and collects size information for
// every file it can read. It is strictly read-only: the only syscalls issued
// are directory reads and stats. Nothing is ever created, modified, or deleted.
package scanner

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// File describes one file found during a scan.
type File struct {
	Path         string
	Name         string
	DiskUsage    int64 // bytes actually allocated on disk (st_blocks * 512)
	ApparentSize int64 // logical size (st_size)
}

// DirError records a directory that could not be read.
type DirError struct {
	Path string
	Err  error
}

// Options configures a Scan.
type Options struct {
	Workers  int      // worker goroutines; defaults to 2*GOMAXPROCS
	XDev     bool     // stay on one filesystem (skip entries on other st_dev)
	Excludes []string // filepath.Match patterns, tried against each entry's
	// base name and its full path (no "**"; use the bare dir name to prune trees)
	Progress      io.Writer     // if non-nil, a status line is refreshed here until the scan ends
	OnProgress    func(Stats)   // called on each progress tick instead of/in addition to Progress
	ProgressEvery time.Duration // refresh interval; defaults to 250ms
}

// Stats reports what the scan touched.
type Stats struct {
	DirsScanned int64 `json:"dirsScanned"`
	FilesSeen   int64 `json:"filesSeen"`
	BytesDisk   int64 `json:"bytesDisk"`
	Skipped     int64 `json:"skipped"` // dirs excluded via patterns or filtered by XDev
	Other       int64 `json:"other"`   // sockets, fifos, devices: counted but not sized
}

// Result holds the outcome of a scan. Partial results are returned when the
// context is cancelled mid-scan; Err explains why the scan stopped early.
type Result struct {
	Root   string
	Files  []File
	Errors []DirError
	Stats  Stats
	Err    error
}

type dirTask struct {
	path string
	dev  int64
}

type counters struct {
	dirs    atomic.Int64
	files   atomic.Int64
	skipped atomic.Int64
	other   atomic.Int64
	bytes   atomic.Int64
}

// walker holds the shared state a single directory walk step needs.
type walker struct {
	ctx     context.Context
	matcher *excluder
	xdev    bool
	rootDev int64
	links   *hardlinkSet
	c       *counters
	q       *taskQueue
	out     chan<- File
}

// Scan walks root and everything below it. Cancelling ctx stops the scan and
// yields whatever was collected so far in Result (with Err set to ctx.Err()).
func Scan(ctx context.Context, root string, opts Options) (*Result, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", root, err)
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}

	w := &walker{
		ctx:     ctx,
		matcher: newExcluder(opts.Excludes),
		xdev:    opts.XDev,
		rootDev: devOf(info),
		links:   newHardlinkSet(),
		c:       &counters{},
	}

	res := &Result{Root: root}

	var resMu sync.Mutex

	files := make(chan File, 512)
	q := newTaskQueue(dirTask{path: root, dev: w.rootDev})
	w.q = q
	w.out = files

	writerDone := startWriter(files, res, &resMu)
	stopProgress := startProgress(ctx, opts.Progress, opts.OnProgress, opts.ProgressEvery, w.c)

	workers := opts.Workers
	if workers < 1 {
		workers = defaultWorkers()
	}

	runWorkers(workers, w, func(t dirTask, walkErr error) {
		resMu.Lock()
		defer resMu.Unlock()

		if t.path == root {
			res.Err = fmt.Errorf("read %s: %w", t.path, walkErr)
			return
		}

		res.Errors = append(res.Errors, DirError{Path: t.path, Err: walkErr})
	})

	writerDone()
	stopProgress()

	res.Stats = w.c.snapshot()
	if ctx.Err() != nil && res.Err == nil {
		res.Err = ctx.Err()
	}

	return res, nil
}

// startWriter drains the files channel into res until it is closed.
func startWriter(files <-chan File, res *Result, mu *sync.Mutex) func() {
	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer wg.Done()

		for f := range files {
			mu.Lock()

			res.Files = append(res.Files, f)

			mu.Unlock()
		}
	}()

	return wg.Wait
}

// runWorkers pops dir tasks until the queue drains, then closes files.
func runWorkers(workers int, w *walker, record func(dirTask, error)) {
	var wg sync.WaitGroup

	for range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			w.drain(record)
		}()
	}

	wg.Wait()
	close(w.out)
}

// drain is one worker's loop: pop, walk, report, repeat.
func (w *walker) drain(record func(dirTask, error)) {
	for {
		t, ok := w.q.pop()
		if !ok {
			return // queue drained: scan complete
		}

		if err := w.walk(t); err != nil {
			record(t, err)
		}

		w.q.doneOne()
	}
}

// walk reads one directory, emits files to out and enqueues child dirs.
func (w *walker) walk(t dirTask) error {
	select {
	case <-w.ctx.Done():
		return nil // draining the queue after cancellation
	default:
	}

	entries, err := os.ReadDir(t.path)
	if err != nil {
		return fmt.Errorf("readdir %s: %w", t.path, err)
	}

	w.c.dirs.Add(1)

	for _, e := range entries {
		w.visit(filepath.Join(t.path, e.Name()), e)
	}

	return nil
}

// visit classifies one directory entry and accounts for it.
func (w *walker) visit(path string, e os.DirEntry) {
	switch {
	case e.IsDir():
		w.visitDir(path, e)
	case e.Type().IsRegular():
		w.visitFile(path, e)
	default:
		// Symlinks are never followed (loop safety) and carry no size;
		// sockets, fifos, and devices are counted but not sized.
		w.c.other.Add(1)
	}
}

func (w *walker) visitDir(path string, e os.DirEntry) {
	info, err := e.Info()
	if err != nil {
		w.c.other.Add(1)
		return
	}

	dev := devOf(info)
	if w.matcher.match(e.Name(), path) || (w.xdev && dev != w.rootDev) {
		w.c.skipped.Add(1)
		return
	}

	w.q.push(dirTask{path: path, dev: dev})
}

func (w *walker) visitFile(path string, e os.DirEntry) {
	info, err := e.Info()
	if err != nil {
		w.c.other.Add(1)
		return
	}

	if statOf(info) != nil && w.links.seen(devOf(info), inoOf(info)) {
		return // hardlink already counted
	}

	w.c.files.Add(1)

	disk := diskUsageOf(info)
	w.c.bytes.Add(disk)

	w.out <- File{
		Path:         path,
		Name:         e.Name(),
		DiskUsage:    disk,
		ApparentSize: info.Size(),
	}
}

func (c *counters) snapshot() Stats {
	return Stats{
		DirsScanned: c.dirs.Load(),
		FilesSeen:   c.files.Load(),
		BytesDisk:   c.bytes.Load(),
		Skipped:     c.skipped.Load(),
		Other:       c.other.Load(),
	}
}

func startProgress(ctx context.Context, w io.Writer, fn func(Stats), every time.Duration, c *counters) func() {
	if w == nil && fn == nil {
		return func() {}
	}

	if every <= 0 {
		every = 250 * time.Millisecond
	}

	done := make(chan struct{})
	stopped := make(chan struct{})

	var once sync.Once

	go func() {
		defer close(stopped)

		ticker := time.NewTicker(every)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				tickProgress(fn, w, c)
			}
		}
	}()

	return func() {
		once.Do(func() { close(done) })
		<-stopped // ensure no progress write races the clear below

		if w != nil {
			// Clearing the status line; a failed clear is unactionable.
			_, _ = fmt.Fprint(w, "\r\033[K")
		}
	}
}

func tickProgress(fn func(Stats), w io.Writer, c *counters) {
	if fn != nil {
		fn(c.snapshot())
	}

	if w != nil {
		// Best-effort status refresh on a terminal; write errors are ignored.
		_, _ = fmt.Fprintf(w, "\rscanned %d dirs, %d files, %s...",
			c.dirs.Load(), c.files.Load(), HumanBytes(c.bytes.Load()))
	}
}

// HumanBytes formats a byte count with binary (1024) units, e.g. "1.5 GiB".
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit || n < 0 {
		return fmt.Sprintf("%d B", n)
	}

	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
