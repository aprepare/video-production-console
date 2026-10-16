package videoqc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"video-production-console/internal/refanalysis"
	"video-production-console/internal/store"
)

func mp4Header() []byte {
	return []byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0, 'i', 's', 'o', 'm', 'm', 'p', '4', '2'}
}

func TestUploadFormatsAndSniffing(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		ok   bool
	}{
		{"movie.mp4", mp4Header(), true}, {"MOVIE.MOV", mp4Header(), true},
		{"movie.webm", append([]byte{0x1a, 0x45, 0xdf, 0xa3}, []byte("doctype webm")...), true},
		{"movie.mkv", append([]byte{0x1a, 0x45, 0xdf, 0xa3}, []byte("doctype matroska")...), true},
		{"text.mp4", []byte("not a movie at all"), false}, {"empty.mov", nil, false}, {"file.exe", mp4Header(), false},
		{"spoof.mp4", append([]byte{0x1a, 0x45, 0xdf, 0xa3}, []byte("doctype webm")...), false},
		{"spoof.webm", mp4Header(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := uploadExtension(tc.name, tc.data)
			if (err == nil) != tc.ok {
				t.Fatalf("format allowed=%v err=%v", tc.ok, err)
			}
		})
	}
}

func TestUploadLifecycleUsesSafeNamesAndCleansFailures(t *testing.T) {
	s, repo, _, root := serviceFixture(t, nil)
	filename := `C:\elsewhere\` + strings.Repeat("海", 150) + "\x01.mp4"
	a, err := s.StartUpload(t.Context(), Input{Product: "蓝盒海鲜", Model: "override"}, filename, bytes.NewReader(mp4Header()))
	if err != nil || a.Status != "queued" || a.Source != "upload" || a.SourceID != "" || a.Model != "override" || utf8.RuneCountInString(a.Title) > 120 || strings.ContainsAny(a.Title, "\\/\x01") {
		t.Fatalf("%+v %v", a, err)
	}
	waitCheck(t, repo, a.ID, "completed")
	f, err := s.OpenVideo(t.Context(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(f)
	f.Close()
	if err != nil || !bytes.Equal(raw, mp4Header()) {
		t.Fatalf("video %x %v", raw, err)
	}
	if _, err := os.Stat(filepath.Join(root, "video-qc", a.ID, "source.mp4")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartUpload(t.Context(), Input{}, "bad.mp4", strings.NewReader("invalid")); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatal(err)
	}
	items, err := repo.List(t.Context(), 30)
	if err != nil || len(items) != 1 {
		t.Fatalf("failed upload left a row: %+v %v", items, err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "video-qc"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files remain: %+v %v", entries, err)
	}
	if err := s.Delete(t.Context(), a.ID); err != nil {
		t.Fatal(err)
	}
	entries, err = os.ReadDir(filepath.Join(root, "video-qc"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("delete cleanup %+v %v", entries, err)
	}
}

type zerosReader struct{}

func (zerosReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestUploadSizeBoundaryAndTemporaryCleanup(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, extra := range []int64{0, 1} {
		size := int64(MaxUploadBytes) + extra
		reader := io.MultiReader(bytes.NewReader(mp4Header()), io.LimitReader(zerosReader{}, size-int64(len(mp4Header()))))
		name := "boundary"
		if extra != 0 {
			name = "oversized"
		}
		_, err := saveUpload(t.Context(), root, name, "sample.mp4", reader)
		if extra == 0 {
			if err != nil {
				t.Fatal(err)
			}
			stat, err := root.Stat(filepath.Join(name, "source.mp4"))
			if err != nil || stat.Size() != MaxUploadBytes {
				t.Fatalf("boundary size %v %v", stat, err)
			}
			if err := root.RemoveAll(name); err != nil {
				t.Fatal(err)
			}
		} else if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("oversized: %v", err)
		}
	}
	entries, err := os.ReadDir(root.Name())
	if err != nil || len(entries) != 0 {
		t.Fatalf("upload leftovers: %v %v", entries, err)
	}
}

func TestRunningDeleteWaitsForCancellationAndCleanup(t *testing.T) {
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	release := make(chan struct{})
	s, repo, _, root := serviceFixture(t, func(opt *Options) {
		opt.Inspect = func(ctx context.Context, _, _, _ string) (refanalysis.VideoInfo, []float64, error) {
			close(entered)
			<-ctx.Done()
			close(cancelled)
			<-release
			return refanalysis.VideoInfo{}, nil, ctx.Err()
		}
	})
	a, err := s.Start(t.Context(), Input{Source: "reference", SourceID: "ref"})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	finished := make(chan error, 1)
	go func() { finished <- s.Delete(context.Background(), a.ID) }()
	<-cancelled
	select {
	case err := <-finished:
		t.Fatalf("deleted before worker exits: %v", err)
	default:
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(t.Context(), a.ID); !errors.Is(err, store.ErrVideoQCNotFound) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "video-qc", a.ID)); !os.IsNotExist(err) {
		t.Fatalf("directory remains %v", err)
	}
}

func TestTaskTimeoutAndFailureRedaction(t *testing.T) {
	for _, kind := range []string{"timeout", "secret"} {
		t.Run(kind, func(t *testing.T) {
			s, repo, _, _ := serviceFixture(t, func(opt *Options) {
				opt.Inspect = func(ctx context.Context, _, _, _ string) (refanalysis.VideoInfo, []float64, error) {
					if kind == "timeout" {
						<-ctx.Done()
						return refanalysis.VideoInfo{}, nil, ctx.Err()
					}
					return refanalysis.VideoInfo{}, nil, errors.New("upstream test-secret at https://model.example/private")
				}
			})
			s.taskTimeout = 20 * time.Millisecond
			a, err := s.Start(t.Context(), Input{Source: "reference", SourceID: "ref"})
			if err != nil {
				t.Fatal(err)
			}
			failed := waitCheck(t, repo, a.ID, "failed")
			if strings.Contains(failed.Error, "test-secret") || strings.Contains(failed.Error, "https://") || failed.Error == "" {
				t.Fatalf("unsafe failure %s", failed.Error)
			}
			if kind == "timeout" && !strings.Contains(failed.Error, "8 分钟") {
				t.Fatal(failed.Error)
			}
		})
	}
}

func TestRootRejectsSymlinksAndManifestTampering(t *testing.T) {
	s, repo, _, root := serviceFixture(t, nil)
	a, err := s.Start(t.Context(), Input{Source: "reference", SourceID: "ref"})
	if err != nil {
		t.Fatal(err)
	}
	waitCheck(t, repo, a.ID, "completed")
	manifest := filepath.Join(root, "video-qc", a.ID, "frames.json")
	if err := os.WriteFile(manifest, []byte(`[{"index":1,"time_s":999}]`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(t.Context(), a.ID); err == nil {
		t.Fatal("corrupt manifest accepted")
	}
	frame := filepath.Join(root, "video-qc", a.ID, "frame_01.jpg")
	if err := os.Remove(frame); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.jpg")
	if err := os.WriteFile(outside, []byte("protected"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, frame); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if f, err := s.OpenFrame(t.Context(), a.ID, 1); err == nil {
		f.Close()
		t.Fatal("followed unsafe frame symlink")
	}
	if err := s.Delete(t.Context(), a.ID); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(outside)
	if err != nil || string(raw) != "protected" {
		t.Fatal("outside file changed")
	}
}
