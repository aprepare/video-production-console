package montageplan

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// statMediaFile is a seam for tests that need to count filesystem probes.
var statMediaFile = os.Stat

// scannedMedia is one index row that passed the cheap field checks, together
// with the filesystem verdict for its absolute path.
type scannedMedia struct {
	item   mediaItem
	usable bool
}

// mediaScan holds everything a media index pass produces that does not depend
// on the caller's strict flag, limit or seed: the rows in index order with
// their stat verdict, and the error that ended the pass. Strict and non-strict
// callers replay the same rows and only differ in how they treat an unusable
// one, so one scan can serve both.
type mediaScan struct {
	rows []scannedMedia
	err  error
	// partial marks a strict pass that stopped at the first unusable clip and
	// therefore never saw the rest of the index.
	partial bool
}

type mediaScanKey struct {
	indexPath string
	mediaRoot string
}

type mediaScanEntry struct {
	modTime time.Time
	size    int64
	scan    *mediaScan
}

var (
	mediaScanMu    sync.Mutex
	mediaScanCache = map[mediaScanKey]mediaScanEntry{}
)

// scanMediaIndex returns the vetted rows of a media index, reusing the last
// pass for the same index file whenever its modtime and size are unchanged.
// stopOnMissing keeps the strict caller's early exit: such a pass is never
// published, so a cached entry always describes the whole index.
func scanMediaIndex(indexPath, mediaRoot string, stopOnMissing bool) (*mediaScan, error) {
	key := mediaScanKey{indexPath: filepath.Clean(indexPath), mediaRoot: filepath.Clean(mediaRoot)}
	before, beforeErr := os.Stat(indexPath)
	if beforeErr == nil {
		if scan := loadMediaScan(key, before); scan != nil {
			return scan, nil
		}
	}
	file, err := os.Open(indexPath)
	if err != nil {
		return nil, fmt.Errorf("open media index: %w", err)
	}
	defer file.Close()
	scan := readMediaScan(file, mediaRoot, stopOnMissing)
	if !scan.partial && beforeErr == nil {
		// Publish only when the index did not move while it was being read,
		// otherwise the rows may describe a half-written file.
		if after, err := file.Stat(); err == nil && sameIndexIdentity(before, after) {
			storeMediaScan(key, after, scan)
		}
	}
	return scan, nil
}

func readMediaScan(r io.Reader, mediaRoot string, stopOnMissing bool) *mediaScan {
	scan := &mediaScan{}
	dec := json.NewDecoder(readerWithoutBOM(r))
	tok, err := dec.Token()
	if err != nil {
		scan.err = err
		return scan
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '[' {
		scan.err = fmt.Errorf("media index must be a JSON array")
		return scan
	}
	for dec.More() {
		var item mediaItem
		if err := dec.Decode(&item); err != nil {
			scan.err = err
			return scan
		}
		// 8s timeline @ 1.1x needs 8.8s source; keep a 1s lead-in margin when possible.
		if item.ID == "" || item.RelativePath == "" || item.DurationSeconds < 10 {
			continue
		}
		item.AbsPath = filepath.Join(mediaRoot, filepath.FromSlash(item.RelativePath))
		info, err := statMediaFile(item.AbsPath)
		usable := err == nil && info.Mode().IsRegular()
		scan.rows = append(scan.rows, scannedMedia{item: item, usable: usable})
		if !usable && stopOnMissing {
			scan.partial = true
			return scan
		}
	}
	end, err := dec.Token()
	if err != nil {
		scan.err = fmt.Errorf("close media index array: %w", err)
		return scan
	}
	if delim, ok := end.(json.Delim); !ok || delim != ']' {
		scan.err = fmt.Errorf("media index array is not closed")
		return scan
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			scan.err = fmt.Errorf("media index contains trailing data")
		} else {
			scan.err = fmt.Errorf("read media index end: %w", err)
		}
	}
	return scan
}

func loadMediaScan(key mediaScanKey, info os.FileInfo) *mediaScan {
	mediaScanMu.Lock()
	defer mediaScanMu.Unlock()
	entry, ok := mediaScanCache[key]
	if !ok || !entry.modTime.Equal(info.ModTime()) || entry.size != info.Size() {
		return nil
	}
	return entry.scan
}

func storeMediaScan(key mediaScanKey, info os.FileInfo, scan *mediaScan) {
	mediaScanMu.Lock()
	defer mediaScanMu.Unlock()
	mediaScanCache[key] = mediaScanEntry{modTime: info.ModTime(), size: info.Size(), scan: scan}
}

func sameIndexIdentity(before, after os.FileInfo) bool {
	return before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}
