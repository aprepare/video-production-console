package monitor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/monitorai"
	"video-production-console/internal/platformapi"
	"video-production-console/internal/refanalysis"
	"video-production-console/internal/settings"
	"video-production-console/internal/store"
)

type References interface {
	Search(context.Context, string, string, string, string, string) (platformapi.SearchPage, error)
	Collect(context.Context, string, *platformapi.Post, string, string, bool) (store.ReferencePost, error)
	ScoreCover(context.Context, string, string) ([]byte, error)
}
type RuntimeProvider interface {
	Runtime(context.Context) (settings.Runtime, error)
}
type Options struct {
	Now        func() time.Time
	Jitter     func(time.Duration) time.Duration
	Sleep      func(context.Context, time.Duration) error
	Interval   time.Duration
	HTTPClient refanalysis.HTTPClient
	Analyze    func(context.Context, string, string) error
	Insight    func(context.Context, string, string) error
}
type queuedRun struct {
	run    store.MonitorRun
	ctx    context.Context
	cancel context.CancelFunc
}
type Service struct {
	repo                     *store.MonitorRepository
	references               References
	referenceRepo            *store.ReferenceRepository
	runtime                  RuntimeProvider
	options                  Options
	model                    *monitorai.Client
	mu                       sync.Mutex
	collectMu                sync.Mutex
	analysisWG               sync.WaitGroup
	jobs                     map[string]*queuedRun
	queue                    []*queuedRun
	wake                     chan struct{}
	life                     context.Context
	cancel                   context.CancelFunc
	done                     chan struct{}
	started, closed, resumed bool
}

func New(repo *store.MonitorRepository, references References, referenceRepo *store.ReferenceRepository, runtime RuntimeProvider, values ...Options) *Service {
	opts := Options{}
	if len(values) > 0 {
		opts = values[0]
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Jitter == nil {
		opts.Jitter = func(max time.Duration) time.Duration { return time.Duration(rand.Int64N(int64(max) + 1)) }
	}
	if opts.Sleep == nil {
		opts.Sleep = func(ctx context.Context, d time.Duration) error {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		}
	}
	if opts.Interval <= 0 {
		opts.Interval = 60 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{repo: repo, references: references, referenceRepo: referenceRepo, runtime: runtime, options: opts, model: monitorai.NewClient(opts.HTTPClient), jobs: map[string]*queuedRun{}, wake: make(chan struct{}, 1), life: ctx, cancel: cancel, done: make(chan struct{})}
}
func (s *Service) jitter(maximum time.Duration) time.Duration {
	return min(maximum, max(time.Duration(0), s.options.Jitter(maximum)))
}
func (s *Service) next(interval int) time.Time {
	return s.options.Now().UTC().Add(time.Duration(interval)*time.Minute + s.jitter(3*time.Minute))
}
func (s *Service) kick() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) Create(ctx context.Context, task store.MonitorTask) (store.MonitorTask, error) {
	if err := ValidateTask(&task); err != nil {
		return store.MonitorTask{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return store.MonitorTask{}, ErrClosed
	}
	task.ID = uuid.NewString()
	task.CreatedAt = stamp(s.options.Now())
	task.UpdatedAt = task.CreatedAt
	task.NextRunAt = task.CreatedAt
	task.LastRunAt, task.LastStatus, task.LastError = "", "", ""
	out, err := s.repo.CreateTask(ctx, task)
	if err == nil {
		s.kick()
	}
	return out, err
}
func (s *Service) update(ctx context.Context, task, previous store.MonitorTask) (store.MonitorTask, error) {
	if err := ValidateTask(&task); err != nil {
		return store.MonitorTask{}, err
	}
	task.UpdatedAt = stamp(s.options.Now())
	task.NextRunAt = previous.NextRunAt
	if task.IntervalMinutes != previous.IntervalMinutes {
		task.NextRunAt = stamp(s.next(task.IntervalMinutes))
	}
	out, err := s.repo.UpdateTask(ctx, task)
	if err == nil {
		s.kick()
	}
	return out, err
}
func (s *Service) Update(ctx context.Context, task store.MonitorTask) (store.MonitorTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return store.MonitorTask{}, ErrClosed
	}
	previous, err := s.repo.GetTask(ctx, task.ID)
	if err != nil {
		return store.MonitorTask{}, err
	}
	return s.update(ctx, task, previous)
}
func (s *Service) Patch(ctx context.Context, id string, patch TaskPatch) (store.MonitorTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return store.MonitorTask{}, ErrClosed
	}
	previous, err := s.repo.GetTask(ctx, id)
	if err != nil {
		return store.MonitorTask{}, err
	}
	task := previous
	patch.Apply(&task)
	return s.update(ctx, task, previous)
}
func (s *Service) Get(ctx context.Context, id string) (store.MonitorTask, error) {
	return s.repo.GetTask(ctx, id)
}
func (s *Service) List(ctx context.Context) ([]store.MonitorTask, error) {
	return s.repo.ListTasks(ctx)
}
func (s *Service) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job := s.jobs[id]; job != nil {
		job.cancel()
	}
	return s.repo.DeleteTask(ctx, id)
}
func (s *Service) Runs(ctx context.Context, id string, limit int) ([]store.MonitorRun, error) {
	if _, err := s.repo.GetTask(ctx, id); err != nil {
		return nil, err
	}
	return s.repo.ListRuns(ctx, id, limit)
}
func (s *Service) Feed(ctx context.Context, id, filter string, limit int) (store.MonitorFeed, error) {
	if filter == "" {
		filter = "all"
	}
	if filter != "all" && filter != "surge" && filter != "high" && filter != "unread" {
		return store.MonitorFeed{}, &ValidationError{"信息流筛选条件不正确"}
	}
	if id != "" {
		if _, err := s.repo.GetTask(ctx, id); err != nil {
			return store.MonitorFeed{}, err
		}
	}
	return s.repo.Feed(ctx, id, filter, limit)
}
func (s *Service) PatchItem(ctx context.Context, id string, read, dismissed *bool) (store.MonitorItem, error) {
	return s.repo.PatchItem(ctx, id, read, dismissed)
}
func (s *Service) ReadAll(ctx context.Context, id string) error {
	if id != "" {
		if _, err := s.repo.GetTask(ctx, id); err != nil {
			return err
		}
	}
	return s.repo.ReadAll(ctx, id)
}

// Resume runs before accepting the first job, including a manual request that
// arrives before the background loop has started. Fresh queued jobs are never
// confused with jobs left by a previous process.
func (s *Service) Resume(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resumed {
		return nil
	}
	if err := s.repo.FailInterrupted(ctx, s.options.Now()); err != nil {
		return err
	}
	s.resumed = true
	return nil
}
func (s *Service) Start(ctx context.Context, id string) (store.MonitorTask, error) {
	return s.enqueue(ctx, id, "manual")
}
func (s *Service) enqueue(ctx context.Context, id, trigger string) (store.MonitorTask, error) {
	if err := s.Resume(ctx); err != nil {
		return store.MonitorTask{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return store.MonitorTask{}, ErrClosed
	}
	task, err := s.repo.GetTask(ctx, id)
	if err != nil {
		return store.MonitorTask{}, err
	}
	if task.Running || s.jobs[id] != nil {
		return store.MonitorTask{}, store.ErrMonitorRunning
	}
	if trigger == "schedule" && (!task.Enabled || task.NextRunAt > stamp(s.options.Now())) {
		return task, nil
	}
	run, err := s.repo.CreateRun(ctx, store.MonitorRun{ID: uuid.NewString(), TaskID: id, Status: "running", Trigger: trigger, StartedAt: stamp(s.options.Now())})
	if err != nil {
		return store.MonitorTask{}, err
	}
	jobCtx, cancel := context.WithCancel(s.life)
	job := &queuedRun{run: run, ctx: jobCtx, cancel: cancel}
	s.jobs[id] = job
	s.queue = append(s.queue, job)
	task.Running = true
	s.kick()
	return task, nil
}
func (s *Service) schedule(ctx context.Context) {
	tasks, err := s.repo.DueTasks(ctx, s.options.Now())
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("读取到期监控任务失败")
		}
		return
	}
	for _, task := range tasks {
		if _, err := s.enqueue(ctx, task.ID, "schedule"); err != nil && !errors.Is(err, store.ErrMonitorRunning) && ctx.Err() == nil {
			slog.Warn("监控排队失败", "task_id", task.ID)
		}
	}
}

// Run owns the only worker. Manual and scheduled runs share this FIFO, so even
// requests for different tasks cannot overlap their platform calls.
func (s *Service) Run(ctx context.Context) {
	s.mu.Lock()
	if s.started || s.closed {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()
	stop := context.AfterFunc(ctx, s.cancel)
	defer stop()
	defer func() {
		s.mu.Lock()
		s.closed = true
		queued := s.queue
		s.queue = nil
		s.mu.Unlock()
		for _, job := range queued {
			job.cancel()
			s.finish(job, errors.New("监控已停止"))
		}
		close(s.done)
	}()
	if err := s.Resume(s.life); err != nil {
		slog.Error("恢复监控状态失败")
		return
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-s.life.Done():
			return
		case <-timer.C:
		case <-s.wake:
		}
		if s.life.Err() != nil {
			return
		}
		s.schedule(s.life)
		for {
			s.mu.Lock()
			if len(s.queue) == 0 {
				s.mu.Unlock()
				break
			}
			job := s.queue[0]
			s.queue = s.queue[1:]
			s.mu.Unlock()
			err := s.execute(job)
			s.finish(job, err)
			job.cancel()
			if s.life.Err() != nil {
				return
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(s.options.Interval)
	}
}
func (s *Service) finish(job *queuedRun, cause error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	defer delete(s.jobs, job.run.TaskID)
	job.run.FinishedAt = stamp(s.options.Now())
	job.run.Status = "completed"
	job.run.Error = ""
	if cause != nil {
		job.run.Status = "failed"
		job.run.Error = cause.Error()
	}
	task, err := s.repo.GetTask(ctx, job.run.TaskID)
	if errors.Is(err, store.ErrMonitorNotFound) {
		return
	}
	if err != nil {
		slog.Error("读取监控完成状态失败", "task_id", job.run.TaskID)
		return
	}
	if err := s.repo.FinishRun(ctx, job.run, s.next(task.IntervalMinutes)); err != nil {
		slog.Error("保存监控执行状态失败", "task_id", task.ID)
	}
	if err := s.repo.Prune(ctx, task.ID, s.options.Now()); err != nil {
		slog.Warn("监控历史清理失败", "task_id", task.ID)
	}
}
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	started := s.started
	var queued []*queuedRun
	if !started {
		queued = s.queue
		s.queue = nil
	}
	s.mu.Unlock()
	if started {
		<-s.done
	} else {
		for _, job := range queued {
			job.cancel()
			s.finish(job, errors.New("监控已停止"))
		}
	}
	s.analysisWG.Wait()
}

type observedItem struct {
	item          store.MonitorItem
	previous      *store.MonitorItem
	decision      surgeDecision
	countsMissing bool
}

func (s *Service) execute(job *queuedRun) error {
	ctx, cancel := context.WithTimeout(job.ctx, 30*time.Minute)
	defer cancel()
	if ctx.Err() != nil {
		return errors.New("监控已停止")
	}
	task, err := s.repo.GetTask(ctx, job.run.TaskID)
	if err != nil {
		return errors.New("监控任务读取失败")
	}
	var cursor, searchID, sessionID string
	posts := []platformapi.Post{}
	seen := map[string]bool{}
	var searchFailure error
	for page := 0; page < task.Pages; page++ {
		if page > 0 {
			if err := s.options.Sleep(ctx, 2*time.Second+s.jitter(3*time.Second)); err != nil {
				return errors.New("监控已停止")
			}
		}
		result, err := s.references.Search(ctx, task.Platform, task.Keyword, cursor, searchID, sessionID)
		if result.LoginWall {
			err = errors.New("抖音搜索需要重新登录，请检查平台登录状态")
		}
		if err != nil {
			cause := searchError(err)
			if page == 0 {
				return cause
			}
			searchFailure = fmt.Errorf("第 %d 页失败：%s（已保存前 %d 页）", page+1, cause, page)
			break
		}
		job.run.Found += int64(len(result.Items))
		for _, post := range result.Items {
			if strings.TrimSpace(post.ExternalID) == "" || seen[post.ExternalID] {
				continue
			}
			seen[post.ExternalID] = true
			post.Platform = platformapi.Platform(task.Platform)
			posts = append(posts, post)
		}
		if !result.HasMore {
			break
		}
		cursor, searchID, sessionID = result.Cursor, result.SearchID, result.SessionID
	}
	observed := make([]observedItem, 0, len(posts))
	for _, post := range posts {
		now := s.options.Now()
		item, previous, err := s.repo.UpsertItem(ctx, task.ID, job.run.ID, post, now)
		if err != nil {
			return errors.New("保存监控条目失败")
		}
		if previous == nil {
			job.run.NewCount++
		}
		// Missing-count observations advance last_seen_at without taking a
		// snapshot. Compare to the last real snapshot, including its timestamp.
		if previous != nil {
			if len(previous.History) == 0 {
				previous = nil
			} else {
				snapshot := previous.History[len(previous.History)-1]
				previous.Likes, previous.Comments = snapshot.Likes, snapshot.Comments
				previous.Shares, previous.Collects = snapshot.Shares, snapshot.Collects
				previous.LastSeenAt = snapshot.At
			}
		}
		d := evaluateSurge(task, item, previous, now)
		if task.SurgeMode == "ai" {
			d.Hit = false
			d.Reason = ""
		}
		observed = append(observed, observedItem{item: item, previous: previous, decision: d, countsMissing: post.CountsMissing})
	}
	if task.SurgeMode != "rule" {
		s.detectAISurges(ctx, task, observed)
	}
	itemIDs := make([]string, 0, len(observed))
	observedIDs := map[string]bool{}
	for i := range observed {
		o := &observed[i]
		itemIDs = append(itemIDs, o.item.ID)
		observedIDs[o.item.ID] = true
		if o.countsMissing {
			continue
		}
		applySurge(&o.item, o.previous, o.decision, s.options.Now())
		if err := s.repo.SetSurge(ctx, o.item.ID, o.item.Surge, o.item.SurgeReason, o.item.SurgeAt, o.item.LikeDelta, o.item.CommentDelta, o.item.LikeRatePerHour); err != nil {
			return errors.New("保存飙升状态失败")
		}
		if o.decision.Hit {
			job.run.SurgeCount++
		}
	}
	if task.ScoreEnabled {
		candidates, err := s.repo.ScoreCandidates(ctx, task.ID, itemIDs)
		if err != nil {
			return errors.New("读取待打分条目失败")
		}
		job.run.ScoredCount = s.scoreItems(ctx, task, candidates)
		// Backlog items should also reach automatic collection when they score.
		for _, item := range candidates {
			if !observedIDs[item.ID] {
				itemIDs = append(itemIDs, item.ID)
			}
		}
	}
	for _, id := range itemIDs {
		if ctx.Err() != nil {
			return errors.New("监控已停止")
		}
		item, err := s.repo.GetItem(ctx, id)
		if err != nil {
			return errors.New("读取监控条目失败")
		}
		if !task.AutoCollect || item.Dismissed || item.ReferenceID != "" || (!item.Surge && item.Score < task.ScoreThreshold) {
			continue
		}
		post, err := s.Collect(ctx, item.ID, task.AutoTranscribe)
		if err != nil {
			slog.Warn("监控自动采集失败", "item_id", item.ID)
			continue
		}
		job.run.CollectedCount++
		if task.AutoAnalyze && item.VideoURL != "" && s.options.Analyze != nil {
			s.startAnalysis(ctx, task, post)
		}
		if task.AutoInsight && s.options.Insight != nil {
			if err := s.options.Insight(ctx, post.ID, task.Model); err != nil {
				slog.Warn("监控评论洞察启动失败", "item_id", item.ID)
			}
		}
	}
	return searchFailure
}

// Collect starts media caching asynchronously. Wait for that local cache before
// calling refanalysis.Start, which requires a readable local video. This wait
// is detached from the run so it neither delays the next task nor affects its
// counters, and is still cancelled and joined during application shutdown.
func (s *Service) startAnalysis(ctx context.Context, task store.MonitorTask, post store.ReferencePost) {
	start := func(ctx context.Context) {
		if err := s.options.Analyze(ctx, post.ID, task.Model); err != nil {
			slog.Warn("监控自动拆解启动失败", "reference_id", post.ID)
		}
	}
	if post.MediaStatus != "downloading" {
		start(ctx)
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.analysisWG.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.analysisWG.Done()
		waitCtx, cancel := context.WithTimeout(s.life, 10*time.Minute)
		defer cancel()
		timer := time.NewTicker(time.Second)
		defer timer.Stop()
		for {
			current, err := s.referenceRepo.Get(waitCtx, post.ID)
			if err != nil {
				if waitCtx.Err() == nil {
					slog.Warn("监控自动拆解原视频读取失败", "reference_id", post.ID)
				}
				return
			}
			switch current.MediaStatus {
			case "ready":
				start(waitCtx)
				return
			case "failed", "none":
				slog.Warn("监控自动拆解原视频未就绪", "reference_id", post.ID)
				return
			}
			select {
			case <-waitCtx.Done():
				return
			case <-timer.C:
			}
		}
	}()
}

// Never persist raw platform errors: URLs and payloads can carry credentials.
func searchError(err error) error {
	if errors.Is(err, platformapi.ErrNotConfigured) {
		return errors.New("请先配置平台网关，再运行监控")
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "461") {
		return errors.New("小红书搜索触发风控（HTTP 461），本轮已停止，请稍后手动检查")
	}
	if strings.Contains(message, "login_wall") || strings.Contains(message, "登录") {
		return errors.New("平台搜索需要重新登录，请检查平台登录状态")
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return errors.New("监控搜索超时或已取消")
	}
	return errors.New("平台搜索失败，请检查平台连接或登录状态；本轮未重试")
}
func itemPost(item store.MonitorItem) platformapi.Post {
	return platformapi.Post{Platform: platformapi.Platform(item.Platform), ExternalID: item.ExternalID, URL: item.URL, Title: item.Title, Text: item.Caption, Author: item.Author, CoverURL: item.CoverURL, VideoURL: item.VideoURL, DurationS: item.DurationS, CreatedAt: item.PostedAt, Likes: item.Likes, Comments: item.Comments, Shares: item.Shares, Collects: item.Collects}
}
func (s *Service) Collect(ctx context.Context, id string, transcribe bool) (store.ReferencePost, error) {
	s.collectMu.Lock()
	defer s.collectMu.Unlock()
	item, err := s.repo.GetItem(ctx, id)
	if err != nil {
		return store.ReferencePost{}, err
	}
	if item.ReferenceID != "" {
		post, err := s.referenceRepo.Get(ctx, item.ReferenceID)
		if err == nil {
			return post, nil
		}
		if !errors.Is(err, store.ErrReferenceNotFound) {
			return store.ReferencePost{}, err
		}
	}
	task, err := s.repo.GetTask(ctx, item.TaskID)
	if err != nil {
		return store.ReferencePost{}, err
	}
	post := itemPost(item)
	saved, err := s.references.Collect(ctx, "", &post, task.Keyword, "", transcribe)
	if err != nil {
		return store.ReferencePost{}, errors.New("素材采集失败，请检查平台连接")
	}
	if err := s.repo.SetReference(ctx, id, saved.ID); err != nil {
		return store.ReferencePost{}, err
	}
	return saved, nil
}
