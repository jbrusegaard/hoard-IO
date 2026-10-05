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
	Workers int  // worker goroutines; defaults to 2*GOMAXPROCS
	XDev    bool // stay on one filesystem (skip entries on other st_dev);
	// unix only - platforms without device ids ignore it
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
func Scan(ctx context.Context, root string, opts *Options) (*Result, error) {
	if opts == nil {
		opts = &Options{}
	}

	root = filepath.Clean(root) // task paths are built by concat, so root keeps no trailing separator

	isDir, rootDev, err := statRoot(root)
	if err != nil {
		return nil, err
	}

	if !isDir {
		return nil, fmt.Errorf("%s is not a directory", root)
	}

	w := &walker{
		ctx:     ctx,
		matcher: newExcluder(opts.Excludes),
		xdev:    opts.XDev && supportsDev,
		rootDev: rootDev,
		links:   newHardlinkSet(),
		c:       &counters{},
	}

	res := &Result{Root: root}

	var resMu sync.Mutex

	files := make(chan File, 512)
	q := newTaskQueue(dirTask{path: root})
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

// drain is one worker's loop: pop, walk, report, repeat. Counts are
// accumulated locally and merged once per directory: per-file atomics on
// shared cache lines cost more than the merge does.
func (w *walker) drain(record func(dirTask, error)) {
	var t tally

	for {
		task, ok := w.q.pop()
		if !ok {
			w.c.merge(t)

			return // queue drained: scan complete
		}

		if err := w.walk(task, &t); err != nil {
			record(task, err)
		}

		w.q.doneOne()
		w.c.merge(t)

		t = tally{}
	}
}

// walk reads one directory, emits files to out and enqueues child dirs.
func (w *walker) walk(task dirTask, t *tally) error {
	select {
	case <-w.ctx.Done():
		return nil // draining the queue after cancellation
	default:
	}

	f, err := os.Open(task.path)
	if err != nil {
		return fmt.Errorf("readdir %s: %w", task.path, err)
	}

	defer func() { _ = f.Close() }() // read-only: nothing to flush, a failed close is unactionable

	// (*File).ReadDir skips the name sort os.ReadDir performs; the report
	// sorts by size anyway and the walk order is irrelevant.
	entries, err := f.ReadDir(-1)
	if err != nil {
		return fmt.Errorf("readdir %s: %w", task.path, err)
	}

	t.dirs++

	d := dirCtx{path: task.path, fd: dirFD(f)}

	for _, e := range entries {
		w.visit(d, e, t)
	}

	return nil
}

// sep is the platform path separator; string(filepath.Separator) is a constant
// expression, so joining stays a plain concatenation.
const sep = string(filepath.Separator)

// joinPath appends a directory entry name to an already-clean directory path.
// filepath.Join's Clean pass is pure overhead here: entry names never contain
// separators, "." or "..". A drive root ("C:\") keeps its trailing separator
// after Clean, and Windows collapses the doubled one, so that is harmless.
func joinPath(dir, name string) string { return dir + sep + name }

// dirCtx carries the two things every entry of one directory needs: its path
// prefix and the handle already open on it. fd is a real descriptor only on
// platforms with fstatat (see stat_unix.go); elsewhere it is noFD and stats go
// by path.
type dirCtx struct {
	path string
	fd   int
}

// visit classifies one directory entry and accounts for it.
func (w *walker) visit(d dirCtx, e os.DirEntry, t *tally) {
	switch {
	case e.IsDir():
		w.visitDir(d, e.Name(), t)
	case e.Type().IsRegular():
		w.visitFile(d, e.Name(), t)
	default:
		// Symlinks are never followed (loop safety) and carry no size;
		// sockets, fifos, and devices are counted but not sized.
		t.other++
	}
}

// visitDir prunes or enqueues a subdirectory. The stat is only needed for the
// cross-device check; without -xdev the entry type from readdir is enough.
func (w *walker) visitDir(d dirCtx, name string, t *tally) {
	path := joinPath(d.path, name)
	if w.matcher.match(name, path) {
		t.skipped++

		return
	}

	if w.xdev {
		info, err := d.statEntry(name)
		if err != nil {
			t.other++

			return
		}

		if info.dev != w.rootDev {
			t.skipped++

			return
		}
	}

	w.q.push(dirTask{path: path})
}

func (w *walker) visitFile(d dirCtx, name string, t *tally) {
	info, err := d.statEntry(name)
	if err != nil {
		t.other++ // entry vanished between readdir and stat

		return
	}

	// nlink 1 means the inode cannot appear anywhere else: skip the contended
	// dedupe map for the overwhelming majority of files. Platforms without
	// inodes report nlink 1, so the map stays untouched there too.
	if info.nlink > 1 && w.links.seen(info.dev, info.ino) {
		return // hardlink already counted
	}

	t.files++

	t.bytes += info.disk

	w.out <- File{
		Path:         joinPath(d.path, name),
		Name:         name,
		DiskUsage:    info.disk,
		ApparentSize: info.size,
	}
}

// tally is a worker-local counter batch.
type tally struct {
	dirs    int64
	files   int64
	skipped int64
	other   int64
	bytes   int64
}

func (c *counters) merge(t tally) {
	if t.dirs == 0 && t.files == 0 && t.skipped == 0 && t.other == 0 && t.bytes == 0 {
		return
	}

	c.dirs.Add(t.dirs)
	c.files.Add(t.files)
	c.skipped.Add(t.skipped)
	c.other.Add(t.other)
	c.bytes.Add(t.bytes)
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
