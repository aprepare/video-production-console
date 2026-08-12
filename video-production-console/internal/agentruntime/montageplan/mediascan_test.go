package montageplan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func resetMediaScanCache(t *testing.T) {
	t.Helper()
	mediaScanMu.Lock()
	mediaScanCache = map[mediaScanKey]mediaScanEntry{}
	mediaScanMu.Unlock()
}

// countMediaStats swaps the stat seam for a counter and restores it afterwards.
func countMediaStats(t *testing.T) *int64 {
	t.Helper()
	var calls int64
	previous := statMediaFile
	statMediaFile = func(path string) (os.FileInfo, error) {
		atomic.AddInt64(&calls, 1)
		return previous(path)
	}
	t.Cleanup(func() { statMediaFile = previous })
	return &calls
}

// writeMediaIndex creates one clip file per id plus the index that lists them.
// ids prefixed with "!" are indexed but never written to disk.
func writeMediaIndex(t *testing.T, root, indexPath string, ids ...string) {
	t.Helper()
	items := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		name := id
		missing := false
		if len(id) > 0 && id[0] == '!' {
			name = id[1:]
			missing = true
		}
		if !missing {
			if err := os.WriteFile(filepath.Join(root, name+".mp4"), []byte(name), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		items = append(items, map[string]any{
			"id": name, "category": "Nature_Landscape",
			"relative_path": name + ".mp4", "duration_seconds": 20,
		})
	}
	raw, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mediaIDs(clips []mediaItem) []string {
	out := make([]string, 0, len(clips))
	for _, clip := range clips {
		out = append(out, clip.ID)
	}
	return out
}

// sortedMediaIDs drops the seeded ranking so a test can assert pool membership.
func sortedMediaIDs(clips []mediaItem) []string {
	out := mediaIDs(clips)
	sort.Strings(out)
	return out
}

func assertSameIDs(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("clip count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("clip %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSampleMediaReusesVettedPoolAcrossPreflightAndBuild(t *testing.T) {
	root := t.TempDir()
	indexPath := filepath.Join(root, "index.json")
	writeMediaIndex(t, root, indexPath, "a", "b", "c", "d")
	resetMediaScanCache(t)
	calls := countMediaStats(t)

	if err := ValidateMediaLibrary(indexPath, root, ""); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	preflight := atomic.LoadInt64(calls)
	if preflight != 4 {
		t.Fatalf("preflight stat calls = %d, want 4", preflight)
	}

	cached, err := sampleMedia(indexPath, root, 4, "task-one", false)
	if err != nil {
		t.Fatalf("cached sampling: %v", err)
	}
	if again := atomic.LoadInt64(calls); again != preflight {
		t.Fatalf("build pass re-stat media: %d calls, want %d", again, preflight)
	}

	resetMediaScanCache(t)
	fresh, err := sampleMedia(indexPath, root, 4, "task-one", false)
	if err != nil {
		t.Fatalf("fresh sampling: %v", err)
	}
	if after := atomic.LoadInt64(calls); after != preflight+4 {
		t.Fatalf("uncached pass stat calls = %d, want %d", after, preflight+4)
	}
	assertSameIDs(t, mediaIDs(cached), mediaIDs(fresh))
}

func TestMediaScanCacheInvalidatesWhenIndexChanges(t *testing.T) {
	root := t.TempDir()
	indexPath := filepath.Join(root, "index.json")
	writeMediaIndex(t, root, indexPath, "a", "b")
	resetMediaScanCache(t)
	calls := countMediaStats(t)

	first, err := sampleMedia(indexPath, root, 8, "task", false)
	if err != nil {
		t.Fatalf("first sampling: %v", err)
	}
	assertSameIDs(t, sortedMediaIDs(first), []string{"a", "b"})
	afterFirst := atomic.LoadInt64(calls)

	// Grown index: different size.
	writeMediaIndex(t, root, indexPath, "a", "b", "c")
	grown, err := sampleMedia(indexPath, root, 8, "task", false)
	if err != nil {
		t.Fatalf("sampling after growth: %v", err)
	}
	if len(grown) != 3 {
		t.Fatalf("clip count after growth = %d, want 3", len(grown))
	}
	afterGrowth := atomic.LoadInt64(calls)
	if afterGrowth != afterFirst+3 {
		t.Fatalf("stat calls after growth = %d, want %d", afterGrowth, afterFirst+3)
	}

	// Same bytes, newer modtime.
	info, err := os.Stat(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	touched := info.ModTime().Add(2 * time.Second)
	if err := os.Chtimes(indexPath, touched, touched); err != nil {
		t.Fatal(err)
	}
	touchedClips, err := sampleMedia(indexPath, root, 8, "task", false)
	if err != nil {
		t.Fatalf("sampling after touch: %v", err)
	}
	if got := atomic.LoadInt64(calls); got != afterGrowth+3 {
		t.Fatalf("stat calls after touch = %d, want %d", got, afterGrowth+3)
	}
	assertSameIDs(t, mediaIDs(touchedClips), mediaIDs(grown))
}

func TestMediaScanCacheKeepsStrictAndNonStrictSemantics(t *testing.T) {
	root := t.TempDir()
	indexPath := filepath.Join(root, "index.json")
	writeMediaIndex(t, root, indexPath, "a", "!gone", "b")
	resetMediaScanCache(t)

	clips, err := sampleMedia(indexPath, root, 8, "task", false)
	if err != nil {
		t.Fatalf("non-strict sampling must skip the missing clip: %v", err)
	}
	assertSameIDs(t, sortedMediaIDs(clips), []string{"a", "b"})

	// The strict caller now reads the same cached rows.
	err = ValidateMediaLibrary(indexPath, root, "")
	if err == nil {
		t.Fatal("preflight must reject a missing indexed clip")
	}
	if want := "media index clip is missing or not a regular file: " + filepath.Join(root, "gone.mp4"); err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}

	// A strict pass that stops early must not publish its partial rows.
	resetMediaScanCache(t)
	if err := ValidateMediaLibrary(indexPath, root, ""); err == nil {
		t.Fatal("preflight must reject a missing indexed clip")
	}
	clips, err = sampleMedia(indexPath, root, 8, "task", false)
	if err != nil {
		t.Fatalf("non-strict sampling after failed preflight: %v", err)
	}
	assertSameIDs(t, sortedMediaIDs(clips), []string{"a", "b"})
}

func TestMediaScanCacheIsConcurrencySafe(t *testing.T) {
	root := t.TempDir()
	indexPath := filepath.Join(root, "index.json")
	writeMediaIndex(t, root, indexPath, "a", "b", "c", "d")
	resetMediaScanCache(t)

	want, err := sampleMedia(indexPath, root, 4, "task", false)
	if err != nil {
		t.Fatal(err)
	}
	resetMediaScanCache(t)

	var wg sync.WaitGroup
	results := make([][]string, 8)
	errs := make([]error, 8)
	for i := 0; i < len(results); i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			clips, err := sampleMedia(indexPath, root, 4, "task", false)
			results[i], errs[i] = mediaIDs(clips), err
		}(i)
	}
	wg.Wait()
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		assertSameIDs(t, results[i], mediaIDs(want))
	}
}
