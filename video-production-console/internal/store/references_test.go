package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestReferenceMediaMigrationUpgradesVersion41(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	for i, migration := range migrations[:41] {
		if _, err := db.Exec(migration); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, i+1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO reference_posts(id,platform,external_id,created_at,updated_at) VALUES('legacy','douyin','1',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"media_status", "media_path", "media_size", "media_error", "cover_path"} {
		if got := scalar(t, db, `SELECT COUNT(*) FROM pragma_table_info('reference_posts') WHERE name=? AND "notnull"=1`, column); got != "1" {
			t.Errorf("missing non-null column %s", column)
		}
	}
	if got := scalar(t, db, `SELECT media_status || ':' || media_path || ':' || media_size || ':' || media_error || ':' || cover_path FROM reference_posts WHERE id='legacy'`); got != "none::0::" {
		t.Fatalf("legacy media defaults=%q", got)
	}
	if got := scalar(t, db, `SELECT COUNT(*) FROM schema_migrations WHERE version=42`); got != "1" {
		t.Fatalf("migration 42 count=%s", got)
	}
}

func TestReferenceRecollectPreservesMedia(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewReferenceRepository(db)
	ctx := context.Background()
	first, err := repo.Upsert(ctx, ReferencePost{ID: "one", Platform: "douyin", ExternalID: "1", VideoURL: "https://cdn/old"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE reference_posts SET media_status='ready',media_path='source.mp4',media_size=12,media_error='retained',cover_path='cover.jpg' WHERE id=?`, first.ID); err != nil {
		t.Fatal(err)
	}
	again, err := repo.Upsert(ctx, ReferencePost{ID: "two", Platform: "douyin", ExternalID: "1", VideoURL: "https://cdn/new", Likes: 99})
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID || again.VideoURL != "https://cdn/new" || again.Likes != 99 {
		t.Fatalf("recollect=%+v", again)
	}
	if got := scalar(t, db, `SELECT media_status || ':' || media_path || ':' || media_size || ':' || media_error || ':' || cover_path FROM reference_posts WHERE id=?`, first.ID); got != "ready:source.mp4:12:retained:cover.jpg" {
		t.Fatalf("media lost on recollect: %s", got)
	}
}

func TestReferenceJSONIncludesTimelineAndHidesPrivateFields(t *testing.T) {
	for _, tc := range []struct {
		name, transcript, sentences string
	}{
		{"valid", `[{"start_ms":10,"end_ms":900,"text":"口播"}]`, `[{"start_ms":10,"end_ms":900,"text":"口播"}]`},
		{"empty", "", `[]`},
		{"null", "null", `[]`},
		{"invalid", `[{"start_ms":10},{"start_ms":"bad"}]`, `[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			post := ReferencePost{VideoURL: "https://cdn/private?signature=secret", CoverURL: "/api/img?u=cover", MediaPath: "private-source", CoverPath: "private-cover", TranscriptJSON: tc.transcript, TranscriptTaskID: "private-task"}
			raw, err := json.Marshal(post)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if string(got["has_video"]) != "true" || string(got["has_cover"]) != "true" || string(got["sentences"]) != tc.sentences {
				t.Fatalf("JSON=%s", raw)
			}
			for _, private := range []string{"video_url", "media_path", "cover_path", "transcript_json", "transcript_task_id", "signature", "private-task", "private-source", "private-cover"} {
				if strings.Contains(string(raw), private) {
					t.Errorf("JSON exposes %s", private)
				}
			}
		})
	}
}

func TestReferenceJSONAvailabilityUsesCachedMedia(t *testing.T) {
	for _, tc := range []struct {
		name         string
		post         ReferencePost
		video, cover bool
	}{
		{"none", ReferencePost{}, false, false},
		{"remote", ReferencePost{VideoURL: "https://cdn/video", CoverURL: "/api/img?u=1"}, true, true},
		{"cached", ReferencePost{MediaStatus: "ready", CoverPath: "cover.jpg"}, true, true},
		{"failed", ReferencePost{MediaStatus: "failed", MediaPath: "source.mp4"}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.post)
			if err != nil {
				t.Fatal(err)
			}
			var flags struct {
				HasVideo bool `json:"has_video"`
				HasCover bool `json:"has_cover"`
			}
			if err := json.Unmarshal(raw, &flags); err != nil {
				t.Fatal(err)
			}
			if flags.HasVideo != tc.video || flags.HasCover != tc.cover || tc.post.HasVideo() != tc.video {
				t.Fatalf("availability=%s", raw)
			}
		})
	}
}
