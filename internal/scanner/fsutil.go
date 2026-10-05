package scanner

import (
	"path/filepath"
	"runtime"
	"sync"
)

func defaultWorkers() int { return 2 * runtime.GOMAXPROCS(0) }

// entryInfo is the stat data the walk needs for one directory entry. The
// platform files (stat_unix.go, stat_other.go) fill it from whichever syscall
// they have; fields the platform cannot report stay zero.
type entryInfo struct {
	dev   int64  // device id, widened to keep comparisons exact
	ino   uint64 // inode / file id; zero means "cannot dedupe hardlinks"
	nlink int64  // hard link count; 1 or less means dedupe cannot apply
	disk  int64  // bytes actually allocated on disk
	size  int64  // logical size
}

type hardkey struct {
	dev int64
	ino uint64
}

// hardlinkSet deduplicates files sharing (device, inode).
type hardlinkSet struct {
	mu  sync.Mutex
	set map[hardkey]struct{}
}

func newHardlinkSet() *hardlinkSet { return &hardlinkSet{set: make(map[hardkey]struct{})} }

// seen reports whether this identity was already observed, recording it if not.
func (h *hardlinkSet) seen(dev int64, ino uint64) bool {
	if ino == 0 { // no usable inode: cannot dedupe
		return false
	}

	k := hardkey{dev, ino}

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, dup := h.set[k]; dup {
		return true
	}

	h.set[k] = struct{}{}

	return false
}

// BadPatterns returns patterns that filepath.Match rejects.
func BadPatterns(patterns []string) []string {
	var bad []string

	for _, p := range patterns {
		if _, err := filepath.Match(p, "x"); err != nil {
			if _, err2 := filepath.Match(p, "x/y"); err2 != nil {
				bad = append(bad, p)
			}
		}
	}

	return bad
}

// excluder matches paths against filepath.Match patterns. A pattern hits if it
// matches the base name ("node_modules") or the full path ("*/vendor/*").
type excluder struct {
	patterns []string
}

func newExcluder(patterns []string) *excluder {
	bad := map[string]bool{}
	for _, p := range BadPatterns(patterns) {
		bad[p] = true
	}

	e := &excluder{}

	for _, p := range patterns {
		if !bad[p] {
			e.patterns = append(e.patterns, p)
		}
	}

	return e
}

func (e *excluder) match(name, path string) bool {
	for _, p := range e.patterns {
		if ok, _ := filepath.Match(p, name); ok {
			return true
		}

		if ok, _ := filepath.Match(p, path); ok {
			return true
		}
	}

	return false
}
