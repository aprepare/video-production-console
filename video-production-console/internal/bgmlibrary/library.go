// Package bgmlibrary scans a local directory of music files and derives the
// timing windows the montage planner needs: how much of the head is usable
// before the quiet tail, and where a loud "climax" segment sits for seamless
// splicing when the video outlasts the track.
package bgmlibrary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const indexSchemaVersion = 1

// IndexFileName is the on-disk cache below the console data root.
const IndexFileName = "bgm_index.json"

// Track is one analyzed music file. All windows are in seconds.
type Track struct {
	ID              string    `json:"id"` // first 16 hex chars of the file SHA-256
	Name            string    `json:"name"`
	Path            string    `json:"path"`
	SHA256          string    `json:"sha256"`
	Size            int64     `json:"size"`
	DurationS       float64   `json:"duration_s"`
	UsableHeadS     float64   `json:"usable_head_s"`
	ClimaxStartS    float64   `json:"climax_start_s"`
	ClimaxDurationS float64   `json:"climax_duration_s"`
	AnalyzedAt      time.Time `json:"analyzed_at"`
}

type Index struct {
	SchemaVersion int     `json:"schema_version"`
	Dir           string  `json:"dir"`
	Tracks        []Track `json:"tracks"`
}

var audioExtensions = map[string]bool{
	".mp3": true, ".m4a": true, ".wav": true, ".flac": true, ".aac": true, ".ogg": true,
}

// LoadIndex reads the cached index; a missing or unreadable file is an empty
// index because a rescan can always rebuild it.
func LoadIndex(indexPath string) Index {
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		return Index{SchemaVersion: indexSchemaVersion}
	}
	var index Index
	if json.Unmarshal(raw, &index) != nil || index.SchemaVersion != indexSchemaVersion {
		return Index{SchemaVersion: indexSchemaVersion}
	}
	return index
}

// FindTrack resolves a configured bgm_id against the index.
func FindTrack(index Index, id string) (Track, bool) {
	for _, track := range index.Tracks {
		if track.ID == id {
			return track, true
		}
	}
	return Track{}, false
}

// Rescan walks the top level of dir, analyzes new or changed audio files with
// ffmpeg and rewrites the index. Unchanged files (same SHA-256) reuse their
// cached analysis so a rescan after adding one song stays fast.
func Rescan(ctx context.Context, dir, ffmpegPath, indexPath string) (Index, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return Index{SchemaVersion: indexSchemaVersion}, fmt.Errorf("bgm directory is not configured")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Index{SchemaVersion: indexSchemaVersion}, fmt.Errorf("read bgm directory: %w", err)
	}
	previous := LoadIndex(indexPath)
	cached := make(map[string]Track, len(previous.Tracks))
	for _, track := range previous.Tracks {
		cached[track.SHA256] = track
	}
	next := Index{SchemaVersion: indexSchemaVersion, Dir: dir}
	var failures []string
	for _, entry := range entries {
		if entry.IsDir() || !audioExtensions[strings.ToLower(filepath.Ext(entry.Name()))] {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		digest, size, err := hashFile(path)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", entry.Name(), err))
			continue
		}
		if track, ok := cached[digest]; ok {
			track.Name, track.Path, track.Size = entry.Name(), path, size
			next.Tracks = append(next.Tracks, track)
			continue
		}
		profile, err := loudnessProfile(ctx, ffmpegPath, path)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", entry.Name(), err))
			continue
		}
		analysis := analyzeWindows(profile)
		next.Tracks = append(next.Tracks, Track{
			ID: digest[:16], Name: entry.Name(), Path: path, SHA256: digest, Size: size,
			DurationS:    analysis.DurationS,
			UsableHeadS:  analysis.UsableHeadS,
			ClimaxStartS: analysis.ClimaxStartS, ClimaxDurationS: analysis.ClimaxDurationS,
			AnalyzedAt: time.Now().UTC(),
		})
	}
	sort.Slice(next.Tracks, func(i, j int) bool { return next.Tracks[i].Name < next.Tracks[j].Name })
	if err := writeIndex(indexPath, next); err != nil {
		return next, err
	}
	if len(failures) > 0 {
		return next, fmt.Errorf("some files could not be analyzed: %s", strings.Join(failures, "; "))
	}
	return next, nil
}

func writeIndex(indexPath string, index Index) error {
	encoded, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o700); err != nil {
		return err
	}
	return os.WriteFile(indexPath, append(encoded, '\n'), 0o600)
}

func hashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hasher := sha256.New()
	size, err := io.Copy(hasher, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}
