package scanner

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Dir-heavy fixture: 60 x 100 = 6000 dirs, 3 files each (~18k entries, 25% dirs),
// approximating a $HOME-like dir:file ratio more closely than the wide fixture.
const (
	dhFixtureRoot = "/tmp/hoardio-bench-dhir-v1"
	dhL1          = 60
	dhL2          = 100
	dhFiles       = 3
)

var (
	dhOnce sync.Once
	dhErr  error
)

func buildDHFixture() error {
	if _, err := os.Stat(filepath.Join(dhFixtureRoot, ".complete")); err == nil {
		return nil
	}
	os.RemoveAll(dhFixtureRoot)
	block := make([]byte, 1024)
	if _, err := rand.Read(block); err != nil {
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
	for i := 0; i < dhL1; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l1 := filepath.Join(dhFixtureRoot, fmt.Sprintf("d%02d", i))
			for j := 0; j < dhL2; j++ {
				dir := filepath.Join(l1, fmt.Sprintf("s%02d", j))
				if err := os.MkdirAll(dir, 0o755); err != nil {
					fail(err)
					return
				}
				for k := 0; k < dhFiles; k++ {
					p := filepath.Join(dir, fmt.Sprintf("f%d.bin", k))
					f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
					if err != nil {
						fail(err)
						return
					}
					f.Write(block)
					f.Close()
				}
			}
		}(i)
	}
	wg.Wait()
	if firstErr != nil {
		os.RemoveAll(dhFixtureRoot)
		return firstErr
	}
	return os.WriteFile(filepath.Join(dhFixtureRoot, ".complete"), []byte("ok"), 0o644)
}

func setupDHFixture(b *testing.B) string {
	b.Helper()
	dhOnce.Do(func() { dhErr = buildDHFixture() })
	if dhErr != nil {
		b.Skipf("fixture unavailable: %v", dhErr)
	}
	return dhFixtureRoot
}

func BenchmarkScanDirHeavy(b *testing.B) {
	root := setupDHFixture(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := Scan(ctx, root, &Options{})
		if err != nil || res.Err != nil {
			b.Fatal(err)
		}
	}
}
