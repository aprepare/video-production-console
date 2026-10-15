package references

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"video-production-console/internal/platformapi"
)

func TestIsHEICRecognizesISOBrands(t *testing.T) {
	for _, brand := range []string{"heic", "heix", "mif1", "heif"} {
		data := append([]byte{0, 0, 0, 24}, []byte("ftyp"+brand+"\x00\x00\x00\x00")...)
		if !IsHEIC(data) {
			t.Errorf("brand %s not recognized", brand)
		}
	}
	if IsHEIC([]byte("ordinary jpeg")) {
		t.Fatal("ordinary data detected as HEIC")
	}
}

func TestPreviewCoverCachesSupportedImageAndEnforcesLimit(t *testing.T) {
	var calls int
	jpegBytes := []byte{0xff, 0xd8, 0xff, 0xe0, 0, 1}
	platform := &fakePlatform{open: func(_ context.Context, _ platformapi.Platform, _ string, _ string) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"image/jpeg"}}, Body: io.NopCloser(bytes.NewReader(jpegBytes))}, nil
	}}
	svc, _ := newService(t, platform)
	for i := 0; i < 2; i++ {
		cover, err := svc.PreviewCover(context.Background(), "xhs", "https://ci.xhscdn.com/a.jpg")
		if err != nil || cover.ContentType != "image/jpeg" || !bytes.Equal(cover.Data, jpegBytes) {
			t.Fatalf("cover=%+v err=%v", cover, err)
		}
	}
	if calls != 1 {
		t.Fatalf("upstream calls=%d want 1", calls)
	}

	platform.open = func(_ context.Context, _ platformapi.Platform, _ string, _ string) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(io.LimitReader(strings.NewReader(strings.Repeat("x", (15<<20)+1)), (15<<20)+1))}, nil
	}
	if _, err := svc.PreviewCover(context.Background(), "xhs", "https://ci.xhscdn.com/too-large"); !errors.Is(err, platformapi.ErrMediaTooLarge) {
		t.Fatalf("limit err=%v", err)
	}
}

func TestPreviewCoverValidatesPlatformBeforeCacheLookup(t *testing.T) {
	var calls int
	platform := &fakePlatform{open: func(_ context.Context, _ platformapi.Platform, _ string, _ string) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader([]byte{0xff, 0xd8, 0xff, 0xe0, 0, 1}))}, nil
	}}
	svc, _ := newService(t, platform)
	rawURL := "https://ci.xhscdn.com/a.jpg"
	if _, err := svc.PreviewCover(context.Background(), "xhs", rawURL); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PreviewCover(context.Background(), "douyin", rawURL); !errors.Is(err, platformapi.ErrMediaHostNotAllowed) {
		t.Fatalf("cross-platform cached preview err=%v", err)
	}
	if calls != 1 {
		t.Fatalf("upstream calls=%d", calls)
	}
}

func TestAVIFCompatibleMIF1RemainsBrowserNative(t *testing.T) {
	data := append([]byte{0, 0, 0, 24}, []byte("ftypmif1\x00\x00\x00\x00avif")...)
	if !isAVIF(data) || needsCoverConversion(data) {
		t.Fatalf("AVIF-compatible mif1 misclassified: avif=%v convert=%v", isAVIF(data), needsCoverConversion(data))
	}
}

func TestCoverLRUEvictsByCountAndBytes(t *testing.T) {
	cache := newCoverLRU(2, 5)
	cache.Add("a", PreviewCover{Data: []byte("12"), ContentType: "image/jpeg"})
	cache.Add("b", PreviewCover{Data: []byte("34"), ContentType: "image/jpeg"})
	if _, ok := cache.Get("a"); !ok {
		t.Fatal("recent entry missing")
	}
	cache.Add("c", PreviewCover{Data: []byte("56"), ContentType: "image/jpeg"})
	if _, ok := cache.Get("b"); ok {
		t.Fatal("least-recent entry was not evicted by count")
	}
	cache.Add("large", PreviewCover{Data: []byte("12345"), ContentType: "image/jpeg"})
	if _, ok := cache.Get("a"); ok {
		t.Fatal("old entry was not evicted by byte limit")
	}
	if got, ok := cache.Get("large"); !ok || string(got.Data) != "12345" {
		t.Fatalf("large=%+v ok=%v", got, ok)
	}
}

func TestServicePreviewCacheDefaultsAndEvictsThe301stEntry(t *testing.T) {
	svc := New(nil, fakeRuntime{})
	if svc.previews.maxItems != 300 || svc.previews.maxBytes != 60<<20 {
		t.Fatalf("defaults items=%d bytes=%d", svc.previews.maxItems, svc.previews.maxBytes)
	}
	for i := 0; i < 301; i++ {
		svc.previews.Add(fmt.Sprintf("key-%03d", i), PreviewCover{Data: []byte{byte(i)}, ContentType: "image/jpeg"})
	}
	if _, ok := svc.previews.Get("key-000"); ok {
		t.Fatal("oldest of 301 entries remained in the default 300-entry cache")
	}
	if got, ok := svc.previews.Get("key-300"); !ok || len(got.Data) != 1 || got.Data[0] != byte(300%256) {
		t.Fatalf("newest entry=%v ok=%v", got.Data, ok)
	}
	if len(svc.previews.items) != 300 || svc.previews.bytes != 300 {
		t.Fatalf("cache items=%d bytes=%d", len(svc.previews.items), svc.previews.bytes)
	}
}

func TestScoreCoverReturnsJPEGWithin512Pixels(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	var png bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 800, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 800; x++ {
			img.Set(x, y, color.RGBA{R: 120, G: 80, B: 40, A: 255})
		}
	}
	// JPEG input proves the reusable score path also rescales supported covers.
	if err := jpeg.Encode(&png, img, nil); err != nil {
		t.Fatal(err)
	}
	platform := &fakePlatform{open: func(_ context.Context, _ platformapi.Platform, _ string, _ string) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(png.Bytes()))}, nil
	}}
	svc, _ := newService(t, platform)
	svc.runtime = fakeRuntime{root: t.TempDir(), ffmpeg: ffmpeg}
	out, err := svc.ScoreCover(context.Background(), "xhs", "https://ci.xhscdn.com/a.jpg")
	if err != nil {
		t.Fatal(err)
	}
	got, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("not jpeg: %v", err)
	}
	if got.Bounds().Dx() > 512 || got.Bounds().Dy() > 512 || got.Bounds().Dx() != 512 {
		t.Fatalf("dimensions=%v", got.Bounds())
	}
}

func TestPreviewCoverConvertsHEICWithRealFFmpeg(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	dir := t.TempDir()
	// A generated HEVC image exercises the actual HEIC decoder, not an AVIF
	// image with a replaced container brand. See testdata/README.md.
	heic, err := os.ReadFile(filepath.Join("testdata", "cover.heic"))
	if err != nil || len(heic) < 12 || string(heic[4:8]) != "ftyp" {
		t.Fatalf("fixture err=%v bytes=%d", err, len(heic))
	}
	if !IsHEIC(heic) {
		t.Fatal("generated HEIC brand was not detected")
	}
	platform := &fakePlatform{open: func(_ context.Context, _ platformapi.Platform, _ string, _ string) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(heic))}, nil
	}}
	svc, _ := newService(t, platform)
	svc.runtime = fakeRuntime{root: dir, ffmpeg: ffmpeg}
	cover, err := svc.PreviewCover(context.Background(), "xhs", "https://ci.xhscdn.com/a.heic")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(cover.Data))
	if err != nil || cover.ContentType != "image/jpeg" {
		t.Fatalf("content_type=%q decode=%v", cover.ContentType, err)
	}
	if decoded.Bounds().Dx() != 540 || decoded.Bounds().Dy() != 360 {
		t.Fatalf("dimensions=%v", decoded.Bounds())
	}
}

func TestCoverConversionCannotFetchURLsEmbeddedInUntrustedBytes(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstream.Close()
	svc, _ := newService(t, &fakePlatform{})
	svc.runtime = fakeRuntime{root: t.TempDir(), ffmpeg: ffmpeg}
	playlist := "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:5\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:5,\n" + upstream.URL + "/segment.ts\n#EXT-X-ENDLIST\n"
	if _, err := svc.convertCoverJPEG(t.Context(), []byte(playlist), 540); err == nil {
		t.Fatal("playlist cannot be a cover")
	}
	if requests.Load() != 0 {
		t.Fatalf("cover conversion bypassed the media allowlist: %d requests", requests.Load())
	}
}
