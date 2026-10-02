package scanner

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// Bench fixture under /tmp, reused across runs via a marker file.
// Layout: 40 x 50 = 2000 leaf dirs, 30 files each (~60k files), ~1% hardlinks.
const (
	fixtureRoot   = "/tmp/hoardio-bench-fixture-v1"
	fixtureL1     = 40
	fixtureL2     = 50
	fixtureFiles  = 30
	fixtureLeaves = fixtureL1 * fixtureL2
)

var (
	fixtureOnce sync.Once
	fixtureErr  error
)

func buildFixture() error {
	if _, err := os.Stat(filepath.Join(fixtureRoot, ".complete")); err == nil {
		return nil
	}
	os.RemoveAll(fixtureRoot)
	block := make([]byte, 4096)
	if _, err := rand.Read(block); err != nil { // random so APFS does not compress/sparse
		return err
	}
	var mu sync.Mutex
	var firstErr error
	fail := func(e error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = e
		}
		mu.Unlock()
	}
	var wg sync.WaitGroup
	for i := 0; i < fixtureL1; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l1 := filepath.Join(fixtureRoot, fmt.Sprintf("d%02d", i))
			for j := 0; j < fixtureL2; j++ {
				dir := filepath.Join(l1, fmt.Sprintf("s%02d", j))
				if err := os.MkdirAll(dir, 0o755); err != nil {
					fail(err)
					return
				}
				for k := 0; k < fixtureFiles; k++ {
					p := filepath.Join(dir, fmt.Sprintf("f%03d.bin", k))
					f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
					if err != nil {
						fail(err)
						return
					}
					off := (k * 137) % len(block)
					f.Write(append(block[off:], block[:off]...)) // vary content, defeat compression
					f.Close()
				}
				if err := os.Link(filepath.Join(dir, "f000.bin"), filepath.Join(dir, "dup_f000.bin")); err != nil {
					fail(err)
				}
			}
		}(i)
	}
	wg.Wait()
	if firstErr != nil {
		os.RemoveAll(fixtureRoot)
		return firstErr
	}
	return os.WriteFile(filepath.Join(fixtureRoot, ".complete"), []byte("ok"), 0o644)
}

func setupFixture(b *testing.B) string {
	b.Helper()
	fixtureOnce.Do(func() { fixtureErr = buildFixture() })
	if fixtureErr != nil {
		b.Skipf("fixture unavailable: %v", fixtureErr)
	}
	return fixtureRoot
}

func BenchmarkScanFixture(b *testing.B) {
	root := setupFixture(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := Scan(ctx, root, Options{})
		if err != nil {
			b.Fatal(err)
		}
		if res.Err != nil || res.Stats.FilesSeen < fixtureLeaves*fixtureFiles {
			b.Fatalf("scan incomplete: %+v", res.Stats)
		}
	}
}

func BenchmarkScanWorkers(b *testing.B) {
	root := setupFixture(b)
	ctx := context.Background()
	for _, w := range []int{1, 2, 4, 8, 16, 32} {
		b.Run(fmt.Sprint(w), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := Scan(ctx, root, Options{Workers: w}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkReadDirOnly is the syscall floor: same tree, readdir only, no Info.
func BenchmarkReadDirOnly(b *testing.B) {
	root := setupFixture(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		stack := []string{root}
		n := 0
		for len(stack) > 0 {
			d := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			entries, err := os.ReadDir(d)
			if err != nil {
				b.Fatal(err)
			}
			for _, e := range entries {
				if e.IsDir() {
					stack = append(stack, d+"/"+e.Name())
				} else if e.Type().IsRegular() {
					n++
				}
			}
		}
		if n < fixtureLeaves*fixtureFiles {
			b.Fatalf("saw %d files", n)
		}
	}
}

// BenchmarkEntryInfo measures os.ReadDir + e.Info() on one dir: each Info is a
// lazy Lstat(parent+"/"+name) in the stdlib (extra path alloc + syscall).
func BenchmarkEntryInfo(b *testing.B) {
	root := setupFixture(b)
	dir := filepath.Join(root, "d00", "s00") // 31 entries incl. dup link
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		entries, err := os.ReadDir(dir)
		if err != nil {
			b.Fatal(err)
		}
		var sum int64
		for _, e := range entries {
			info, err := e.Info()
			if err != nil {
				b.Fatal(err)
			}
			sum += info.Size()
		}
		if sum == 0 {
			b.Fatal("no sizes")
		}
	}
}

// BenchmarkLstatReuse measures Lstat on an already-joined path (what walkDir
// could do instead of e.Info(), reusing its path string, same syscall count).
func BenchmarkLstatReuse(b *testing.B) {
	root := setupFixture(b)
	dir := filepath.Join(root, "d00", "s00")
	names, err := os.ReadDir(dir)
	if err != nil {
		b.Fatal(err)
	}
	paths := make([]string, len(names))
	for i, e := range names {
		paths[i] = dir + "/" + e.Name()
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var sum int64
		for _, p := range paths {
			info, err := os.Lstat(p)
			if err != nil {
				b.Fatal(err)
			}
			sum += info.Size()
		}
		if sum == 0 {
			b.Fatal("no sizes")
		}
	}
}

func BenchmarkPathJoinVsConcat(b *testing.B) {
	dir := "/Users/tester/Library/Containers/com.apple.Safari/Data/Library/Caches"
	name := "urlcache-hash-entry-0123456789.bin"
	if filepath.Join(dir, name) != dir+"/"+name {
		b.Fatal("precondition: results differ")
	}
	b.Run("filepath.Join", func(b *testing.B) {
		b.ReportAllocs()
		var sink string
		for i := 0; i < b.N; i++ {
			sink = filepath.Join(dir, name)
		}
		_ = sink
	})
	b.Run("concat", func(b *testing.B) {
		b.ReportAllocs()
		var sink string
		for i := 0; i < b.N; i++ {
			sink = dir + "/" + name
		}
		_ = sink
	})
}

func BenchmarkHardlinkSeenSerial(b *testing.B) {
	h := newHardlinkSet()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if h.seen(1, uint64(i)) {
			b.Fatal("unexpected dup")
		}
	}
}

func BenchmarkHardlinkSeenParallel(b *testing.B) {
	h := newHardlinkSet()
	var base atomic.Int64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			h.seen(1, uint64(base.Add(1)))
		}
	})
}

func BenchmarkExcluderMatchEmpty(b *testing.B) {
	e := newExcluder(nil)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if e.match("f001.bin", "/tmp/a/b/f001.bin") {
			b.Fatal("unexpected match")
		}
	}
}
