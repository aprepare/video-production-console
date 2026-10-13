package references

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"video-production-console/internal/platformapi"
	"video-production-console/internal/store"
)

var ErrNoReferenceVideo = errors.New("no_reference_video")

const mediaLimit int64 = 1 << 30

type mediaDownload struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

// CacheMedia downloads a source synchronously; concurrent calls share its state.
func (s *Service) CacheMedia(ctx context.Context, id string) (store.ReferencePost, error) {
	post, job, err := s.beginMedia(ctx, ctx, id)
	if err != nil || job == nil {
		return post, err
	}
	return s.cacheMedia(post, job)
}

// StartMediaCache reserves the download before returning and detaches its lifetime.
func (s *Service) StartMediaCache(ctx context.Context, id string) (store.ReferencePost, error) {
	workCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	post, job, err := s.beginMedia(ctx, workCtx, id)
	if err != nil || job == nil {
		cancel()
		return post, err
	}
	go func() {
		defer cancel()
		_, _ = s.cacheMedia(post, job)
	}()
	return post, nil
}

func (s *Service) beginMedia(ctx, workCtx context.Context, id string) (store.ReferencePost, *mediaDownload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleting[id] {
		return store.ReferencePost{}, nil, store.ErrReferenceNotFound
	}
	post, err := s.repo.Get(ctx, id)
	if err != nil {
		return post, nil, err
	}
	if s.downloads[id] != nil {
		return post, nil, nil
	}
	if strings.TrimSpace(post.VideoURL) == "" {
		return post, nil, ErrNoReferenceVideo
	}
	if err := s.repo.MarkMediaDownloading(ctx, id); err != nil {
		return post, nil, err
	}
	post, err = s.repo.Get(ctx, id)
	if err != nil {
		return post, nil, err
	}
	jobCtx, cancel := context.WithCancel(workCtx)
	job := &mediaDownload{ctx: jobCtx, cancel: cancel, done: make(chan struct{})}
	s.downloads[id] = job
	return post, job, nil
}

func (s *Service) cacheMedia(post store.ReferencePost, job *mediaDownload) (store.ReferencePost, error) {
	defer func() {
		job.cancel()
		s.mu.Lock()
		delete(s.downloads, post.ID)
		close(job.done)
		s.mu.Unlock()
	}()
	if err := s.cacheMediaFiles(job.ctx, post); err != nil {
		message := mediaErrorMessage(err, post.Platform)
		// A canceled download still needs a durable terminal state.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.repo.FailMedia(ctx, post.ID, message); err != nil {
			return post, errors.New(message + "；缓存状态保存失败")
		}
		post, _ = s.repo.Get(ctx, post.ID)
		return post, errors.New(message)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.repo.Get(ctx, post.ID)
}

func (s *Service) cacheMediaFiles(ctx context.Context, post store.ReferencePost) error {
	if !validMediaID(post.ID) {
		return errors.New("invalid reference media ID")
	}
	root, rootPath, err := s.mediaRoot(ctx, true)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.MkdirAll(post.ID, 0o755); err != nil {
		return err
	}
	dir, err := root.OpenRoot(post.ID)
	if err != nil {
		return err
	}
	defer dir.Close()
	client, err := s.client(ctx)
	if err != nil {
		return err
	}
	size, err := downloadMediaFile(ctx, client, platformapi.Platform(post.Platform), post.VideoURL, dir, "source.mp4", mediaLimit)
	if err != nil {
		return err
	}
	if err := s.repo.CompleteMedia(ctx, post.ID, filepath.Join(rootPath, post.ID, "source.mp4"), size); err != nil {
		return err
	}
	// A slow or failed cover must not hide an already cached video.
	coverCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	coverPath := filepath.Join(rootPath, post.ID, "cover.jpg")
	covered := false
	if strings.TrimSpace(post.CoverURL) != "" {
		_, err := downloadMediaFile(coverCtx, client, platformapi.Platform(post.Platform), post.CoverURL, dir, "cover.jpg", 20<<20)
		covered = err == nil
	}
	if !covered {
		// Some platforms expose no fetchable cover; a frame of the source works everywhere.
		covered = s.coverFromSource(coverCtx, filepath.Join(rootPath, post.ID, "source.mp4"), coverPath) == nil
	}
	if covered {
		_ = s.repo.SetCoverPath(coverCtx, post.ID, coverPath)
	}
	return nil
}

// coverFromSource writes one JPEG frame from the cached video next to it.
func (s *Service) coverFromSource(ctx context.Context, source, target string) error {
	runtime, err := s.runtime.Runtime(ctx)
	if err != nil {
		return err
	}
	ffmpeg := strings.TrimSpace(runtime.FFmpegPath)
	if ffmpeg == "" {
		return errors.New("ffmpeg is not configured")
	}
	part := target + ".part.jpg"
	defer os.Remove(part)
	cmd := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-y", "-ss", "0.5", "-i", source, "-frames:v", "1", "-vf", "scale='min(720,iw)':-2", "-q:v", "4", part)
	if err := cmd.Run(); err != nil {
		return err
	}
	if info, err := os.Stat(part); err != nil || info.Size() == 0 {
		return errors.New("cover frame is empty")
	}
	return os.Rename(part, target)
}

func downloadMediaFile(ctx context.Context, client Platform, platform platformapi.Platform, rawURL string, dir *os.Root, name string, limit int64) (int64, error) {
	part := name + ".part"
	if err := dir.Remove(part); err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	file, err := dir.OpenFile(part, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	defer dir.Remove(part)
	defer file.Close()
	n, err := client.Download(ctx, platform, rawURL, file, limit)
	if err != nil {
		return 0, err
	}
	if n > limit {
		return 0, platformapi.ErrMediaTooLarge
	}
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if info.Size() > limit {
		return 0, platformapi.ErrMediaTooLarge
	}
	if info.Size() == 0 {
		return 0, errors.New("empty reference media")
	}
	if err := file.Close(); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := dir.Rename(part, name); err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func (s *Service) mediaRoot(ctx context.Context, create bool) (*os.Root, string, error) {
	runtime, err := s.runtime.Runtime(ctx)
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(runtime.DataRoot) == "" {
		return nil, "", errors.New("reference data root is not configured")
	}
	dataPath, err := filepath.Abs(runtime.DataRoot)
	if err != nil {
		return nil, "", err
	}
	data, err := os.OpenRoot(dataPath)
	if err != nil {
		return nil, "", err
	}
	defer data.Close()
	if create {
		if err := data.MkdirAll("references", 0o755); err != nil {
			return nil, "", err
		}
	}
	root, err := data.OpenRoot("references")
	return root, filepath.Join(dataPath, "references"), err
}

// OpenMedia validates stored absolute paths and confines symlinks to the cache.
func (s *Service) OpenMedia(ctx context.Context, path string) (*os.File, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("invalid reference media path")
	}
	root, rootPath, err := s.mediaRoot(ctx, false)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	rel, err := filepath.Rel(rootPath, path)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, errors.New("reference media path is outside cache")
	}
	return root.Open(rel)
}

// Delete waits for an active writer before removing its directory and record.
func (s *Service) Delete(ctx context.Context, id string) error {
	if !validMediaID(id) {
		return errors.New("对标缓存路径无效。")
	}
	s.mu.Lock()
	if s.deleting[id] {
		s.mu.Unlock()
		return store.ErrReferenceNotFound
	}
	if _, err := s.repo.Get(ctx, id); err != nil {
		s.mu.Unlock()
		return err
	}
	s.deleting[id] = true
	job := s.downloads[id]
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.deleting, id)
		s.mu.Unlock()
	}()
	if job != nil {
		job.cancel()
		select {
		case <-job.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	root, _, err := s.mediaRoot(ctx, false)
	if err != nil && !os.IsNotExist(err) {
		return errors.New("对标缓存目录读取失败。")
	}
	if root != nil {
		defer root.Close()
		if err := root.RemoveAll(id); err != nil {
			return errors.New("对标缓存删除失败。")
		}
	}
	return s.repo.Delete(ctx, id)
}

func validMediaID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, "/\\:")
}

func mediaErrorMessage(err error, platform string) string {
	var status *platformapi.DownloadHTTPError
	switch {
	case errors.As(err, &status):
		if status.StatusCode == 403 || status.StatusCode == 404 || status.StatusCode == 410 {
			return "视频地址已过期，请重新采集"
		}
		return fmt.Sprintf("%s原视频下载失败（HTTP %d）", platform, status.StatusCode)
	case errors.Is(err, platformapi.ErrMediaTooLarge):
		return "原视频超过 1 GiB，无法缓存"
	case errors.Is(err, platformapi.ErrNotConfigured):
		return "请先在设置里填写平台接口地址和 Token。"
	case errors.Is(err, context.DeadlineExceeded):
		return "原视频下载超时，请重试"
	case errors.Is(err, context.Canceled):
		return "原视频下载已取消，请重试"
	default:
		return "原视频下载失败，请重试"
	}
}
