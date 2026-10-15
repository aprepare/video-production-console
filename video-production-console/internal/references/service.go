// Package references collects 同行 posts from 抖音 / 小红书 / 视频号 through
// the console-api gateway and turns their spoken audio into 二创原文 with
// 通义听悟. Transcription is asynchronous: a job is submitted, its task ID is
// persisted, and a poller finishes it — also after a console restart.
package references

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"video-production-console/internal/platformapi"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

// ErrNoTranscribableVideo means the post carries no playable media URL
// (小红书 image notes, or a post captured before its video was resolved).
var ErrNoTranscribableVideo = errors.New("no_transcribable_video")

// Platform is the subset of the gateway client the service uses; tests
// replace it with a fake.
type Platform interface {
	Status(ctx context.Context) ([]platformapi.AppStatus, error)
	Search(ctx context.Context, platform platformapi.Platform, keyword, cursor, searchID, sessionID string) (platformapi.SearchPage, error)
	Resolve(ctx context.Context, link string) (platformapi.Post, error)
	Comments(ctx context.Context, platform platformapi.Platform, externalID string, limit int) ([]platformapi.Comment, error)
	StartTranscription(ctx context.Context, videoURL string) (platformapi.Transcript, error)
	Transcription(ctx context.Context, taskID string) (platformapi.Transcript, error)
	Download(ctx context.Context, platform platformapi.Platform, rawURL string, w io.Writer, limit int64) (int64, error)
	OpenMedia(ctx context.Context, platform platformapi.Platform, rawURL, rangeHeader string) (*http.Response, error)
}

type RuntimeProvider interface {
	Runtime(context.Context) (consoleSettings.Runtime, error)
}

// Service is safe for concurrent use.
type Service struct {
	repo     *store.ReferenceRepository
	runtime  RuntimeProvider
	factory  func(baseURL, token string) (Platform, error)
	interval time.Duration

	mu        sync.Mutex
	polling   map[string]bool
	wake      chan struct{}
	downloads map[string]*mediaDownload
	deleting  map[string]bool
	coverJPEG func(context.Context, []byte, int) ([]byte, error)
	previews  *coverLRU
}

// New builds a service that creates gateway clients from live settings, so a
// changed URL or token applies without restarting the console.
func New(repo *store.ReferenceRepository, runtime RuntimeProvider) *Service {
	return &Service{
		repo:    repo,
		runtime: runtime,
		factory: func(baseURL, token string) (Platform, error) {
			return platformapi.New(baseURL, token, nil)
		},
		interval:  8 * time.Second,
		polling:   map[string]bool{},
		wake:      make(chan struct{}, 1),
		downloads: map[string]*mediaDownload{},
		deleting:  map[string]bool{},
		previews:  newCoverLRU(300, 60<<20),
	}
}

// WithPlatform replaces the gateway factory (tests).
func (s *Service) WithPlatform(factory func(baseURL, token string) (Platform, error), interval time.Duration) *Service {
	s.factory = factory
	if interval > 0 {
		s.interval = interval
	}
	return s
}

func (s *Service) client(ctx context.Context) (Platform, error) {
	runtime, err := s.runtime.Runtime(ctx)
	if err != nil {
		return nil, err
	}
	return s.factory(runtime.PlatformAPIBaseURL, runtime.PlatformAPIToken)
}

// Status reports the gateway adapters. Not-configured is a normal state.
func (s *Service) Status(ctx context.Context) ([]platformapi.AppStatus, error) {
	client, err := s.client(ctx)
	if err != nil {
		return nil, err
	}
	return client.Status(ctx)
}

// Search proxies one page of platform search; results are not stored until
// the operator collects them.
func (s *Service) Search(ctx context.Context, platform, keyword, cursor, searchID, sessionID string) (platformapi.SearchPage, error) {
	client, err := s.client(ctx)
	if err != nil {
		return platformapi.SearchPage{}, err
	}
	return client.Search(ctx, platformapi.Platform(platform), keyword, cursor, searchID, sessionID)
}

// Collect stores a post. With a link it resolves the share first; with a
// search result it stores the item as given. transcribe submits 听悟 when
// the post has a video.
func (s *Service) Collect(ctx context.Context, link string, post *platformapi.Post, keyword, merchantID string, transcribe bool) (store.ReferencePost, error) {
	client, err := s.client(ctx)
	if err != nil {
		return store.ReferencePost{}, err
	}
	var resolved platformapi.Post
	if post != nil {
		resolved = *post
	} else {
		resolved, err = client.Resolve(ctx, link)
		if err != nil {
			return store.ReferencePost{}, err
		}
	}
	if strings.TrimSpace(resolved.ExternalID) == "" {
		return store.ReferencePost{}, fmt.Errorf("没有识别出这条内容的编号")
	}
	saved, err := s.repo.Upsert(ctx, toRecord(resolved, keyword, merchantID))
	if err != nil {
		return store.ReferencePost{}, err
	}
	if saved.HasVideo() && saved.MediaStatus != "ready" {
		if caching, cacheErr := s.StartMediaCache(ctx, saved.ID); cacheErr == nil {
			saved = caching
		}
	}
	if transcribe && saved.HasVideo() && saved.TranscriptStatus != "completed" && saved.TranscriptStatus != "running" {
		return s.startTranscription(ctx, client, saved)
	}
	return saved, nil
}

// Transcribe (re)submits 听悟 for a stored post.
func (s *Service) Transcribe(ctx context.Context, id string) (store.ReferencePost, error) {
	post, err := s.repo.Get(ctx, id)
	if err != nil {
		return store.ReferencePost{}, err
	}
	if !post.HasVideo() {
		return post, ErrNoTranscribableVideo
	}
	client, err := s.client(ctx)
	if err != nil {
		return store.ReferencePost{}, err
	}
	return s.startTranscription(ctx, client, post)
}

func (s *Service) startTranscription(ctx context.Context, client Platform, post store.ReferencePost) (store.ReferencePost, error) {
	job, err := client.StartTranscription(ctx, post.VideoURL)
	if err != nil {
		_ = s.repo.FailTranscript(ctx, post.ID, err.Error())
		return s.repo.Get(ctx, post.ID)
	}
	if job.Done() {
		if err := s.complete(ctx, post.ID, job); err != nil {
			return store.ReferencePost{}, err
		}
		return s.repo.Get(ctx, post.ID)
	}
	if job.TaskID == "" {
		_ = s.repo.FailTranscript(ctx, post.ID, "听悟没有返回任务编号")
		return s.repo.Get(ctx, post.ID)
	}
	if err := s.repo.MarkTranscriptRunning(ctx, post.ID, job.TaskID); err != nil {
		return store.ReferencePost{}, err
	}
	s.kick()
	return s.repo.Get(ctx, post.ID)
}

func (s *Service) complete(ctx context.Context, id string, job platformapi.Transcript) error {
	sentences, _ := json.Marshal(job.Sentences)
	return s.repo.CompleteTranscript(ctx, id, strings.TrimSpace(job.Text), string(sentences))
}

// Comments returns top comments of a stored post (not persisted).
func (s *Service) Comments(ctx context.Context, id string, limit int) ([]platformapi.Comment, error) {
	post, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	client, err := s.client(ctx)
	if err != nil {
		return nil, err
	}
	return client.Comments(ctx, platformapi.Platform(post.Platform), post.ExternalID, limit)
}

func (s *Service) kick() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Run polls running transcriptions until ctx ends. Start it once.
func (s *Service) Run(ctx context.Context) {
	s.mu.Lock()
	active := make([]string, 0, len(s.downloads))
	for id := range s.downloads {
		active = append(active, id)
	}
	err := s.repo.FailInterruptedMedia(ctx, active)
	s.mu.Unlock()
	if err != nil && ctx.Err() == nil {
		slog.Warn("reference interrupted media reset failed")
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-s.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
		s.pollOnce(ctx)
		timer.Reset(s.interval)
	}
}

// pollOnce advances every running transcription by one status check.
func (s *Service) pollOnce(ctx context.Context) {
	running, err := s.repo.Running(ctx)
	if err != nil || len(running) == 0 {
		return
	}
	client, err := s.client(ctx)
	if err != nil {
		return
	}
	for _, post := range running {
		if ctx.Err() != nil {
			return
		}
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		job, err := client.Transcription(callCtx, post.TranscriptTaskID)
		cancel()
		switch {
		case err != nil:
			// A failed status call is not a failed transcription; give up
			// only after the job has clearly outlived 听悟's own deadline.
			if time.Since(post.UpdatedAt) > 3*time.Hour {
				_ = s.repo.FailTranscript(ctx, post.ID, err.Error())
			}
		case job.Done():
			if completeErr := s.complete(ctx, post.ID, job); completeErr != nil {
				slog.Warn("reference transcript store failed", "reference_id", post.ID, "error", completeErr)
			}
		case job.Status == "FAILED" || job.Status == "INVALID":
			_ = s.repo.FailTranscript(ctx, post.ID, "听悟转写失败（"+job.Status+"）")
		}
	}
}

func toRecord(post platformapi.Post, keyword, merchantID string) store.ReferencePost {
	return store.ReferencePost{
		ID:         uuid.NewString(),
		Platform:   string(post.Platform),
		ExternalID: post.ExternalID,
		URL:        post.URL,
		Title:      post.Title,
		Caption:    post.Text,
		Author:     post.Author,
		CoverURL:   post.CoverURL,
		VideoURL:   post.VideoURL,
		DurationS:  post.DurationS,
		Likes:      post.Likes,
		Comments:   post.Comments,
		Shares:     post.Shares,
		Collects:   post.Collects,
		Plays:      post.Plays,
		PostedAt:   post.CreatedAt,
		Keyword:    strings.TrimSpace(keyword),
		MerchantID: strings.TrimSpace(merchantID),
	}
}
