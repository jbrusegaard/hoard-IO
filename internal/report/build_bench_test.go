package report

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbrusegaard/hoardio/internal/scanner"
)

// genFiles builds a synthetic flat scan result: l1 x l2 dirs, n files each.
func genFiles(root string, l1, l2, n int) []scanner.File {
	files := make([]scanner.File, 0, l1*l2*n)
	for i := 0; i < l1; i++ {
		d1 := fmt.Sprintf("d%02d", i)
		for j := 0; j < l2; j++ {
			d2 := fmt.Sprintf("s%02d", j)
			for k := 0; k < n; k++ {
				name := fmt.Sprintf("f%03d.bin", k)
				rel := d1 + "/" + d2 + "/" + name
				files = append(files, scanner.File{
					Path:         root + "/" + rel,
					Name:         name,
					DiskUsage:    int64(512 * ((k % 8) + 1)),
					ApparentSize: int64(256 * ((k % 4) + 1)),
				})
			}
		}
	}
	return files
}

var benchRoot = "/tmp/hoardio-bench-home"
var benchFiles = genFiles(benchRoot, 40, 50, 30) // ~60k files, 2000 dirs

func BenchmarkBuild(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		t := Build(benchRoot, benchFiles)
		if t.Root.DiskUsage == 0 {
			b.Fatal("empty tree")
		}
	}
}

func BenchmarkSortBySize(b *testing.B) {
	t := Build(benchRoot, benchFiles)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		SortBySize(t.Root)
	}
}

func BenchmarkSortByName(b *testing.B) {
	t := Build(benchRoot, benchFiles)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		SortByName(t.Root)
	}
}

func BenchmarkTopFiles(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		top := topFiles(benchFiles, 30, 0)
		if len(top) != 30 {
			b.Fatalf("top=%d", len(top))
		}
	}
}

// BenchmarkRelCost isolates the filepath.Rel cost that Build pays per file.
func BenchmarkRelCost(b *testing.B) {
	b.ReportAllocs()
	var sink string
	for i := 0; i < b.N; i++ {
		f := benchFiles[i%len(benchFiles)]
		rel, err := filepath.Rel(benchRoot, f.Path)
		if err != nil {
			b.Fatal(err)
		}
		sink = rel
	}
	_ = sink
}

// BenchmarkTrimPrefixCost shows the cheaper alternative Build could use.
func BenchmarkTrimPrefixCost(b *testing.B) {
	pfx := benchRoot + string(filepath.Separator)
	b.ReportAllocs()
	var sink string
	for i := 0; i < b.N; i++ {
		f := benchFiles[i%len(benchFiles)]
		sink = strings.TrimPrefix(f.Path, pfx)
	}
	_ = sink
}
