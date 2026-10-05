//go:build darwin

package scanner

import (
	"context"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// TestXDevSkipsOtherDevices uses the macOS APFS volume split under
// /System/Volumes: Data, Update and Preboot are separate volumes with distinct
// st_dev values. With -xdev the sibling volumes are pruned, so the scan never
// descends into them and stays cheap while the device check runs against real
// st_dev values.
func TestXDevSkipsOtherDevices(t *testing.T) {
	const (
		root    = "/System/Volumes"
		sibling = "/System/Volumes/Update"
	)

	var rootStat, sibStat unix.Stat_t
	if err := unix.Lstat(root, &rootStat); err != nil {
		t.Skipf("cannot stat %s: %v", root, err)
	}

	if err := unix.Lstat(sibling, &sibStat); err != nil {
		t.Skipf("cannot stat %s: %v", sibling, err)
	}

	if int64(sibStat.Dev) == int64(rootStat.Dev) {
		t.Skipf("%s is on the same device as %s: no cross-device pair available", sibling, root)
	}

	res, err := Scan(context.Background(), root, &Options{Workers: 2, XDev: true, Excludes: []string{"Data"}})
	if err != nil {
		t.Fatal(err)
	}

	if res.Err != nil {
		t.Fatalf("scan of %s reported %v", root, res.Err)
	}

	if res.Stats.Skipped == 0 {
		t.Error("Skipped = 0, want sibling volumes pruned by device")
	}

	for _, f := range res.Files {
		if strings.HasPrefix(f.Path, sibling) {
			t.Errorf("file %s from another device leaked into results", f.Path)
		}
	}
}
