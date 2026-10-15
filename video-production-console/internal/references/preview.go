package references

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"video-production-console/internal/platformapi"
)

const previewCoverLimit int64 = 15 << 20

type PreviewCover struct {
	Data        []byte
	ContentType string
}

// OpenPreview streams an allowlisted upstream media response. Redirects are
// rejected by platformapi.Client.OpenMedia.
func (s *Service) OpenPreview(ctx context.Context, platform, rawURL, rangeHeader string) (*http.Response, error) {
	if err := platformapi.ValidateMediaURL(platformapi.Platform(platform), rawURL); err != nil {
		return nil, err
	}
	client, err := s.client(ctx)
	if err != nil {
		return nil, err
	}
	return client.OpenMedia(ctx, platformapi.Platform(platform), rawURL, rangeHeader)
}

// PreviewCover downloads and caches a browser-compatible cover. Unsupported
// formats (including HEIC/HEIF) are converted to JPEG with a 540px max edge.
func (s *Service) PreviewCover(ctx context.Context, platform, rawURL string) (PreviewCover, error) {
	if err := platformapi.ValidateMediaURL(platformapi.Platform(platform), rawURL); err != nil {
		return PreviewCover{}, err
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(rawURL)))
	if cached, ok := s.previews.Get(key); ok {
		return cached, nil
	}
	data, err := s.downloadCover(ctx, platform, rawURL)
	if err != nil {
		return PreviewCover{}, err
	}
	contentType := coverContentType(data)
	if needsCoverConversion(data) {
		data, err = s.convertCoverJPEG(ctx, data, 540)
		if err != nil {
			return PreviewCover{}, err
		}
		contentType = "image/jpeg"
	}
	cover := PreviewCover{Data: data, ContentType: contentType}
	s.previews.Add(key, cover)
	return cover, nil
}

// ScoreCover returns a JPEG whose longest edge is at most 512 pixels for
// model image inputs. Callers may base64-encode the returned bytes directly.
func (s *Service) ScoreCover(ctx context.Context, platform, rawURL string) ([]byte, error) {
	data, err := s.downloadCover(ctx, platform, rawURL)
	if err != nil {
		return nil, err
	}
	return s.convertCoverJPEG(ctx, data, 512)
}

func (s *Service) downloadCover(ctx context.Context, platform, rawURL string) ([]byte, error) {
	resp, err := s.OpenPreview(ctx, platform, rawURL, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.ContentLength > previewCoverLimit {
		return nil, platformapi.ErrMediaTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, previewCoverLimit+1))
	if err != nil {
		return nil, fmt.Errorf("download cover: %w", err)
	}
	if int64(len(data)) > previewCoverLimit {
		return nil, platformapi.ErrMediaTooLarge
	}
	if len(data) == 0 {
		return nil, errors.New("cover is empty")
	}
	return data, nil
}

func (s *Service) convertCoverJPEG(ctx context.Context, data []byte, maxEdge int) ([]byte, error) {
	if s.coverJPEG != nil {
		return s.coverJPEG(ctx, data, maxEdge)
	}
	runtime, err := s.runtime.Runtime(ctx)
	if err != nil {
		return nil, err
	}
	ffmpeg := strings.TrimSpace(runtime.FFmpegPath)
	if ffmpeg == "" {
		return nil, errors.New("ffmpeg is not configured")
	}
	dir, err := os.MkdirTemp("", "reference-cover-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	input, output := filepath.Join(dir, "input.bin"), filepath.Join(dir, "output.jpg")
	if err := os.WriteFile(input, data, 0o600); err != nil {
		return nil, err
	}
	filter := fmt.Sprintf("scale=w='min(%d,iw)':h='min(%d,ih)':force_original_aspect_ratio=decrease", maxEdge, maxEdge)
	cmd := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-y", "-i", input, "-frames:v", "1", "-vf", filter, "-q:v", "4", output)
	if err := cmd.Run(); err != nil {
		return nil, errors.New("cover conversion failed")
	}
	out, err := os.ReadFile(output)
	if err != nil || len(out) == 0 {
		return nil, errors.New("converted cover is empty")
	}
	return out, nil
}

// IsHEIC detects the ISO BMFF brands used by HEIC/HEIF images.
func IsHEIC(data []byte) bool {
	if len(data) < 12 || !bytes.Equal(data[4:8], []byte("ftyp")) {
		return false
	}
	probe := data[8:min(len(data), 64)]
	for _, brand := range []string{"heic", "heix", "hevc", "hevx", "heim", "heis", "hevm", "hevs", "mif1", "msf1", "heif"} {
		if bytes.Contains(probe, []byte(brand)) {
			return true
		}
	}
	return false
}

func isAVIF(data []byte) bool {
	return len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")) && (bytes.Contains(data[8:min(len(data), 64)], []byte("avif")) || bytes.Contains(data[8:min(len(data), 64)], []byte("avis")))
}

func coverContentType(data []byte) string {
	if isAVIF(data) {
		return "image/avif"
	}
	return http.DetectContentType(data[:min(len(data), 512)])
}

func needsCoverConversion(data []byte) bool {
	if isAVIF(data) {
		return false
	}
	if IsHEIC(data) {
		return true
	}
	mime := coverContentType(data)
	return mime != "image/jpeg" && mime != "image/png" && mime != "image/gif" && mime != "image/webp" && mime != "image/avif"
}

type coverCacheEntry struct {
	key   string
	cover PreviewCover
}

type coverLRU struct {
	mu       sync.Mutex
	items    map[string]*list.Element
	order    *list.List
	maxItems int
	maxBytes int64
	bytes    int64
}

func newCoverLRU(maxItems int, maxBytes int64) *coverLRU {
	return &coverLRU{items: make(map[string]*list.Element), order: list.New(), maxItems: maxItems, maxBytes: maxBytes}
}

func (c *coverLRU) Get(key string) (PreviewCover, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element := c.items[key]
	if element == nil {
		return PreviewCover{}, false
	}
	c.order.MoveToFront(element)
	entry := element.Value.(coverCacheEntry)
	entry.cover.Data = bytes.Clone(entry.cover.Data)
	return entry.cover, true
}

func (c *coverLRU) Add(key string, cover PreviewCover) {
	if int64(len(cover.Data)) > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if old := c.items[key]; old != nil {
		entry := old.Value.(coverCacheEntry)
		c.bytes -= int64(len(entry.cover.Data))
		c.order.Remove(old)
		delete(c.items, key)
	}
	copyCover := PreviewCover{Data: bytes.Clone(cover.Data), ContentType: cover.ContentType}
	element := c.order.PushFront(coverCacheEntry{key: key, cover: copyCover})
	c.items[key] = element
	c.bytes += int64(len(copyCover.Data))
	for len(c.items) > c.maxItems || c.bytes > c.maxBytes {
		oldest := c.order.Back()
		entry := oldest.Value.(coverCacheEntry)
		c.bytes -= int64(len(entry.cover.Data))
		delete(c.items, entry.key)
		c.order.Remove(oldest)
	}
}
