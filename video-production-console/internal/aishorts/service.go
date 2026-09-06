package aishorts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"video-production-console/internal/agentruntime/openaicompat"
	"video-production-console/internal/security"
)

func now() time.Time { return time.Now().UTC() }

// Runtime 是服务每次动手时需要的连接信息，由调用方从设置里现取，改设置不用重启。
type Runtime struct {
	BaseURL          string
	APIKey           string
	ImageBaseURL     string
	ImageAPIKey      string
	VideoBaseURL     string
	VideoAPIKey      string
	ImageConcurrency int
	VideoConcurrency int
	MediaResolved    bool
	Models           Models
	JianyingRoot     string
	FFprobePath      string
}

// BrandKit 是一条短片进剪映时借用混剪那套"账号包装"：9:16 背景框、BGM、音效、字幕样式。
// 由 httpapi 从账号覆盖 / 全局设置 / BGM 库 / 机器模板缓存里解出来，assembler 只管用。
type BrandKit struct {
	BackgroundPath string // 账号 9:16 背景图；空则用深色底
	// BGM：路径为空表示不铺。UsableHead/Climax 用来循环（先放头一遍，再反复副歌段）。
	BGMPath            string
	BGMVolume          float64 // 线性音量，混剪验证过的默认 0.2512（-12 dB）
	BGMUsableHeadS     float64
	BGMClimaxStartS    float64
	BGMClimaxDurationS float64
	// SFX：role → 文件路径。opening / whoosh / conclusion / drop，缺哪个就不放哪个。
	SFX       map[string]string
	SFXVolume float64
	// 字幕样式（竖版内嵌布局）。
	CaptionFont    string
	CaptionSize    float64
	CaptionColor   string // #RRGGBB
	CaptionBgColor string // #RRGGBB，空 = 无底色
	CaptionBgAlpha float64
	// 顶部标题（可空）：来自短片标题，混剪叫"板面标题"。
	Title    string
	Subtitle string
}

// DefaultBrandKit 是没有任何账号包装时的兜底：无背景、无 BGM、白字底色字幕。
func DefaultBrandKit() BrandKit {
	return BrandKit{
		BGMVolume: 0.2512, SFXVolume: 0.3981,
		CaptionFont: "新青年体", CaptionSize: 9, CaptionColor: "#FFFFFF", CaptionBgColor: "#C8455C", CaptionBgAlpha: 0.92,
	}
}

type RuntimeProvider func(ctx context.Context) (Runtime, error)

// Service 编排一条短片的全部步骤；长任务在后台 goroutine 里跑，进度写回 Store，
// 前端轮询 Get 就能看到每张卡的状态变化。
type Service struct {
	media    *mediaSettingsStore
	store    *Store
	runtime  RuntimeProvider
	assemble Assembler
	// busy 锁整条短片的批量任务（拆分镜 / 生成全部 / 组装）。
	// shotBusy 只锁某一镜：不同镜头可并行，同一镜不能重复提交。
	busy     sync.Map
	shotBusy sync.Map
	// 生图并发上限（中转站允许 20 张同时出）和生视频并发上限（任务型接口，别压太多）。
	imageGate generationLimiter
	videoGate generationLimiter
}

// Assembler 负责配音 + 剪映草稿，由 assemble.go 实现；抽成接口方便测试替换。
type Assembler interface {
	Assemble(ctx context.Context, rt Runtime, short *Short, assetDir string, progress func(string)) (AssembleResult, error)
}

// NarrationPrewarmer 可选：生图的同时把配音和时间轴先跑出来，组装时直接复用。
type NarrationPrewarmer interface {
	PrewarmNarration(ctx context.Context, rt Runtime, short *Short, assetDir string)
}

type AssembleResult struct {
	NarrationPath string
	SRTPath       string
	DraftPath     string
	DraftName     string
	DurationS     float64
	ShotTimes     [][2]float64
	Captions      []CaptionCue
}

func NewService(dataRoot string, runtime RuntimeProvider, assembler Assembler) *Service {
	return &Service{
		media: &mediaSettingsStore{path: filepath.Join(dataRoot, "ai-short-media-settings.json"), protector: security.NewSecretProtector()},
		store: &Store{DataRoot: dataRoot}, runtime: runtime, assemble: assembler,
	}
}

func (s *Service) Store() *Store { return s.store }

// Create 建一条短片：只存文案，不拆分镜（拆分镜是独立步骤，方便改完文案再拆）。
// mode 为 fable（寓言动画，文案 20～1500 字）或 explainer（财经解说，文案可到 6000 字）。
// textModel 是拆分镜用的模型，空则用默认；segmentModel 是解说模式分大段用的模型，空则机械切。
func (s *Service) Create(accountID, mode, title, story, headline, style, textModel, segmentModel, imageModel string, visual ...*VisualSettings) (*Short, error) {
	return s.CreateWithReasoning(accountID, mode, title, story, headline, style, textModel, segmentModel, imageModel, "", visual...)
}

func (s *Service) CreateWithReasoning(accountID, mode, title, story, headline, style, textModel, segmentModel, imageModel, effort string, visual ...*VisualSettings) (*Short, error) {
	if err := validateReasoning(effort); err != nil {
		return nil, err
	}

	story = strings.TrimSpace(story)
	if mode != ModeExplainer {
		mode = ModeFable
	}
	maxRunes := 1500
	if mode == ModeExplainer {
		maxRunes = 6000
	}
	if n := len([]rune(story)); n < 20 || n > maxRunes {
		return nil, fmt.Errorf("文案需要 20～%d 字", maxRunes)
	}
	if mode == ModeExplainer {
		if strings.TrimSpace(style) == "" {
			style = financeEditorial
		}
		style = StyleByKey(strings.TrimSpace(style)).Key
	} else if strings.TrimSpace(style) == "" {
		style = DefaultStylePrompt
	}
	if strings.TrimSpace(title) == "" {
		title = firstLine(story, 24)
	}
	short := &Short{
		ID: uuid.NewString(), AccountID: strings.TrimSpace(accountID), Mode: mode, Title: strings.TrimSpace(title),
		Headline: strings.TrimSpace(headline), Story: story, Style: strings.TrimSpace(style),
		TextReasoningEffort: strings.TrimSpace(effort), TextModel: strings.TrimSpace(textModel), SegmentModel: strings.TrimSpace(segmentModel), ImageModel: strings.TrimSpace(imageModel),
		Status: StatusDraft, Characters: []Character{}, Shots: []Shot{},
		CreatedAt: now(), UpdatedAt: now(),
	}
	if short.IsExplainer() {
		short.VisualSettings = DefaultVisualSettings()
		if len(visual) > 0 {
			if err := applyVisualSettings(short, visual[0]); err != nil {
				return nil, err
			}
		}
	}
	if err := s.store.Save(short); err != nil {
		return nil, err
	}
	return short, nil
}

func (s *Service) Get(id string) (*Short, error) {
	short, err := s.store.Get(id)
	if err != nil || (short.Status != StatusGenerating && short.Status != StatusAssembling) || !s.tryLock(id) {
		return short, err
	}
	defer s.unlock(id)
	return s.store.Update(id, func(x *Short) error {
		if x.Status != StatusGenerating && x.Status != StatusAssembling {
			return nil
		}
		x.Status, x.Error = StatusFailed, "上次任务已中断，可续跑未完成的镜头；已有素材和视频任务编号已保留。"
		for i := range x.Shots {
			if x.Shots[i].ImageStatus == ShotRunning {
				x.Shots[i].ImageStatus = ShotFailed
			}
			if x.Shots[i].VideoStatus == ShotRunning {
				x.Shots[i].VideoStatus = ShotFailed
			}
		}
		return nil
	})
}
func (s *Service) List() ([]*Short, error) {
	items, err := s.store.List()
	if err != nil {
		return nil, err
	}
	for i, item := range items {
		if item.Status == StatusGenerating || item.Status == StatusAssembling {
			items[i], err = s.Get(item.ID)
			if err != nil {
				return nil, err
			}
		}
	}
	return items, nil
}
func (s *Service) Delete(id string) error { return s.store.Delete(id) }

// UpdateText 改文案/金句/画风/模型（重拆分镜前用）。textModel/segmentModel 传 nil 表示不改，传空串表示改回默认。
func (s *Service) UpdateText(id, title, story, headline, style string, textModel, segmentModel, imageModel *string, visual ...*VisualSettings) (*Short, error) {
	return s.UpdateTextWithReasoning(id, title, story, headline, style, textModel, segmentModel, imageModel, nil, visual...)
}

func (s *Service) UpdateTextWithReasoning(id, title, story, headline, style string, textModel, segmentModel, imageModel, effort *string, visual ...*VisualSettings) (*Short, error) {
	if effort != nil {
		if err := validateReasoning(*effort); err != nil {
			return nil, err
		}
	}

	if !s.tryLock(id) {
		return nil, ErrBusy
	}
	defer s.unlock(id)
	return s.store.Update(id, func(short *Short) error {
		if effort != nil {
			short.TextReasoningEffort = strings.TrimSpace(*effort)
		}
		oldStory, oldHeadline, oldStyle := short.Story, short.Headline, short.Style
		oldLayout := ""
		if short.VisualSettings != nil {
			oldLayout = short.VisualSettings.Layout
		}
		if len(visual) > 0 {
			if err := applyVisualSettings(short, visual[0]); err != nil {
				return err
			}
		}
		if strings.TrimSpace(title) != "" {
			short.Title = strings.TrimSpace(title)
		}
		if strings.TrimSpace(story) != "" {
			short.Story = strings.TrimSpace(story)
		}
		short.Headline = strings.TrimSpace(headline)
		if strings.TrimSpace(style) != "" {
			if short.IsExplainer() {
				nextStyle := StyleByKey(strings.TrimSpace(style)).Key
				if nextStyle != short.Style {
					short.Style = nextStyle
					// 切换项目策略后重新解析每镜画风，旧图保留并标为需要重生。
					for i := range short.Shots {
						shot := &short.Shots[i]
						shot.StyleKey = resolvedShotStyle(nextStyle, shot.StyleKey)
						shot.ImageStale = shot.ImagePath != ""
						shot.VideoPath, shot.VideoStatus, shot.VideoRequestID = "", ShotPending, ""
						shot.Hero, shot.VideoPrompt, shot.Error = false, "", ""
					}
					if len(short.Shots) > 0 {
						short.Status = StatusStoryboard
					}
				}
			} else {
				short.Style = strings.TrimSpace(style)
			}
		}
		if textModel != nil {
			short.TextModel = strings.TrimSpace(*textModel)
		}
		if segmentModel != nil {
			short.SegmentModel = strings.TrimSpace(*segmentModel)
		}
		if imageModel != nil {
			short.ImageModel = strings.TrimSpace(*imageModel)
		}
		fillAllPrompts(short, false)
		if oldStory != short.Story || oldHeadline != short.Headline || oldStyle != short.Style {
			markDraftStale(short)
		}
		if short.IsExplainer() && oldStory != short.Story && len(short.Shots) > 0 {
			short.StoryboardStale = true
		}
		if short.VisualSettings != nil && oldLayout != short.VisualSettings.Layout {
			for i := range short.Shots {
				short.Shots[i].ImageStale = short.Shots[i].ImagePath != ""
			}
		}
		return nil
	})
}

// ShotPatch 是改一镜时可选的字段，空值表示不改（Hero 用指针区分）。
type ShotPatch struct {
	Scene, Motion, Narration, Speaker                 string
	Seconds                                           int
	StyleKey, Subject                                 string
	Hero                                              *bool
	VisualIntent, SubjectType, CameraMove, Annotation *string
	Keywords                                          *[]ShotKeyword
}

// UpdateShot 改某镜的画面/动作/台词/说话人（改画面后通常要重生这一镜；改说话人只影响配音）。
func (s *Service) UpdateShot(id string, index int, patch ShotPatch) (*Short, error) {
	if !s.tryLock(id) {
		return nil, ErrBusy
	}
	defer s.unlock(id)
	scene, motion, narration, speaker, seconds := patch.Scene, patch.Motion, patch.Narration, patch.Speaker, patch.Seconds
	return s.store.Update(id, func(short *Short) error {
		if index < 0 || index >= len(short.Shots) {
			return errors.New("shot index out of range")
		}
		shot := &short.Shots[index]
		oldPrompt := shot.ImagePrompt
		if short.IsExplainer() {
			if patch.SubjectType != nil {
				if !validSubjectType(*patch.SubjectType) {
					return errors.New("画面主体类型无效")
				}
				shot.SubjectType = *patch.SubjectType
			}
			if patch.CameraMove != nil {
				if !validCameraMove(*patch.CameraMove) {
					return errors.New("镜头运动类型无效")
				}
				shot.CameraMove = *patch.CameraMove
			}
			if patch.VisualIntent != nil {
				shot.VisualIntent = strings.TrimSpace(*patch.VisualIntent)
			}
			if patch.Annotation != nil {
				shot.Annotation = strings.TrimSpace(*patch.Annotation)
			}
			if patch.Keywords != nil {
				shot.Keywords = append([]ShotKeyword{}, (*patch.Keywords)...)
			}
			if short.Style == financeEditorial && strings.TrimSpace(patch.StyleKey) != "" {
				if !editorialShotStyle(strings.TrimSpace(patch.StyleKey)) {
					return errors.New("混合画风仅支持纸张拼贴或微缩模型")
				}
				shot.StyleKey = strings.TrimSpace(patch.StyleKey)
			}
			shot.StyleKey = resolvedShotStyle(short.Style, shot.StyleKey)
			if s := strings.TrimSpace(patch.Subject); s != "" {
				shot.Subject = s
			}
			shot.Hero, shot.VideoPrompt = false, ""
		}
		if strings.TrimSpace(scene) != "" {
			shot.Scene = strings.TrimSpace(scene)
		}
		if strings.TrimSpace(motion) != "" {
			shot.Motion = strings.TrimSpace(motion)
		}
		if strings.TrimSpace(narration) != "" {
			if strings.TrimSpace(narration) != shot.Narration {
				for i := range short.Shots {
					short.Shots[i].StartS, short.Shots[i].EndS = 0, 0
				}
			}
			shot.Narration = strings.TrimSpace(narration)
		}
		if speaker = strings.TrimSpace(speaker); speaker != "" {
			if speaker == SpeakerNarrator {
				shot.Speaker = speaker
			} else {
				for _, c := range short.Characters {
					if c.Name == speaker {
						shot.Speaker = speaker
						break
					}
				}
			}
		}
		if seconds == 6 || seconds == 10 || seconds == 15 {
			shot.Seconds = seconds
		}
		if shot.SpokenByCharacter() {
			if need := secondsForLine(shot.Narration); need > shot.Seconds {
				shot.Seconds = need
			}
		}
		fillShotPrompts(short, index)
		if short.IsExplainer() && oldPrompt != shot.ImagePrompt && shot.ImagePath != "" {
			shot.ImageStale = true
		}
		markDraftStale(short)
		return nil
	})
}

func (s *Service) tryLock(id string) bool {
	if s.hasShotBusy(id) {
		return false
	}
	_, loaded := s.busy.LoadOrStore(id, true)
	if loaded {
		return false
	}
	// 和 tryLockShot 的二次检查一起封住并发抢锁的窗口。
	if s.hasShotBusy(id) {
		s.busy.Delete(id)
		return false
	}
	return true
}

func (s *Service) unlock(id string) { s.busy.Delete(id) }

func shotLockKey(id string, index int) string { return fmt.Sprintf("%s:%d", id, index) }

func (s *Service) hasShotBusy(id string) bool {
	prefix := id + ":"
	found := false
	s.shotBusy.Range(func(key, _ any) bool {
		if strings.HasPrefix(key.(string), prefix) {
			found = true
			return false
		}
		return true
	})
	return found
}

func (s *Service) tryLockShot(id string, index int) bool {
	if _, global := s.busy.Load(id); global {
		return false
	}
	key := shotLockKey(id, index)
	if _, loaded := s.shotBusy.LoadOrStore(key, true); loaded {
		return false
	}
	if _, global := s.busy.Load(id); global {
		s.shotBusy.Delete(key)
		return false
	}
	return true
}

func (s *Service) unlockShot(id string, index int) {
	s.shotBusy.Delete(shotLockKey(id, index))
}

var ErrBusy = errors.New("这条短片正在处理中，等它跑完再操作")

func (s *Service) clients(ctx context.Context) (Runtime, *GenClient, openaicompat.ChatClient, error) {
	rt, err := s.runtime(ctx)
	if err != nil {
		return Runtime{}, nil, nil, err
	}
	rt, err = s.mediaRuntime(rt)
	if err != nil {
		return Runtime{}, nil, nil, err
	}
	gen := &GenClient{BaseURL: rt.ImageBaseURL, APIKey: rt.ImageAPIKey}
	chat := &openaicompat.HTTPChatClient{BaseURL: rt.BaseURL, APIKey: rt.APIKey}
	return rt, gen, chat, nil
}

// Storyboard 拆分镜（同步，几十秒）。会清掉已有的镜头与角色图。
func (s *Service) Storyboard(ctx context.Context, id string) (*Short, error) {
	if !s.tryLock(id) {
		return nil, ErrBusy
	}
	defer s.unlock(id)
	short, err := s.store.Get(id)
	if err != nil {
		return nil, err
	}
	rt, _, chat, err := s.clients(ctx)
	if err != nil {
		return nil, err
	}
	textModel := strings.TrimSpace(short.TextModel)
	if textModel == "" {
		textModel = rt.Models.Text
	}
	if short.IsExplainer() {
		err = buildExplainerStoryboard(ctx, chat, textModel, strings.TrimSpace(short.SegmentModel), short)
	} else {
		err = buildStoryboard(chat, textModel, short)
	}
	if err != nil {
		_, _ = s.store.Update(id, func(x *Short) error { x.Error = err.Error(); return nil })
		return nil, err
	}
	return s.store.Update(id, func(x *Short) error {
		x.Headline, x.Characters, x.Shots = short.Headline, short.Characters, short.Shots
		x.Status, x.Error = StatusStoryboard, ""
		if x.IsExplainer() {
			markDraftStale(x)
			x.StoryboardStale = false
		} else {
			x.NarrationPath, x.SRTPath, x.DraftPath, x.DraftName, x.DurationS = "", "", "", "", 0
		}
		fillAllPrompts(x, false)
		return nil
	})
}

// GenerateAll 后台跑：先出全部角色设定图，再并发出每镜首帧 + 视频。
// 已经 done 的镜头跳过，所以失败后再点一次就是"续跑"。
func (s *Service) GenerateAll(id string) error {
	if !s.tryLock(id) {
		return ErrBusy
	}
	short, err := s.store.Get(id)
	if err != nil {
		s.unlock(id)
		return err
	}
	if short.StoryboardStale {
		s.unlock(id)
		return errors.New("文案已更新，请先重新拆分镜")
	}
	if len(short.Shots) == 0 {
		s.unlock(id)
		return errors.New("先拆分镜")
	}
	go func() {
		defer s.unlock(id)
		ctx := context.Background()
		_, _ = s.store.Update(id, func(x *Short) error { x.Status, x.Error = StatusGenerating, ""; return nil })
		rt, gen, _, err := s.clients(ctx)
		if err != nil {
			s.fail(id, err)
			return
		}
		// 解说模式：配音 + 逐字对齐和生图并行跑，组装时命中缓存，省 2～4 分钟。
		if pre, ok := s.assemble.(NarrationPrewarmer); ok && short.IsExplainer() {
			if short.VisualSettings == nil || short.VisualSettings.OpeningVideoSeconds == 0 {
				go pre.PrewarmNarration(ctx, rt, short, s.store.AssetDir(id))
			}
		}
		if err := s.prepareOpeningTimeline(ctx, rt, id); err != nil {
			s.fail(id, err)
			return
		}
		if err := s.generateCharacters(ctx, rt, gen, id); err != nil {
			s.fail(id, err)
			return
		}
		if err := s.generateShots(ctx, rt, gen, id, nil); err != nil {
			s.fail(id, err)
			return
		}
		s.finishGeneration(id)
	}()
	return nil
}

// RegenerateShot 重做单镜：stage = image（重生图并连带重生视频）或 video（只重生视频）。
func (s *Service) RegenerateShot(id string, index int, stage string) error {
	short, err := s.store.Get(id)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(short.Shots) {
		return errors.New("shot index out of range")
	}
	if stage != "image" && stage != "video" {
		return errors.New("生成阶段无效")
	}
	if stage == "video" && !short.NeedsVideo(short.Shots[index]) {
		return errors.New("请先启用开场AI视频，且选择开场范围内的镜头")
	}
	endpointChanged := false
	if stage == "video" && short.Shots[index].VideoRequestID != "" {
		rt, _, _, err := s.clients(context.Background())
		if err != nil {
			return err
		}
		previous := short.Shots[index].VideoRequestBaseURL
		if previous == "" {
			previous = rt.BaseURL
		}
		endpointChanged = strings.TrimRight(previous, "/") != strings.TrimRight(rt.VideoBaseURL, "/")
	}
	if !s.tryLockShot(id, index) {
		return ErrBusy
	}
	// 接口返回前就把这一镜标成 running：前端立刻只禁用这张卡，也防止轮询漏掉。
	if _, err := s.store.Update(id, func(x *Short) error {
		x.Status, x.Error = StatusGenerating, ""
		shot := &x.Shots[index]
		if stage == "image" {
			shot.ImageStatus = ShotRunning
			if !x.IsExplainer() {
				shot.ImagePath = ""
			}
			markDraftStale(x)
			shot.VideoPath, shot.VideoStatus, shot.VideoRequestID = "", ShotPending, ""
		} else {
			// 失败后的重试继续查询已提交任务；成功后的重生才提交新任务。
			if shot.VideoStatus != ShotFailed || endpointChanged {
				shot.VideoRequestID = ""
			}
			shot.VideoStatus = ShotRunning
		}
		shot.VideoSubmitUncertain = false
		shot.Error = ""
		return nil
	}); err != nil {
		s.unlockShot(id, index)
		return err
	}
	go func() {
		defer s.unlockShot(id, index)
		ctx := context.Background()
		rt, gen, _, err := s.clients(ctx)
		if err != nil {
			_, _ = s.store.Update(id, func(x *Short) error {
				shot := &x.Shots[index]
				if stage == "image" {
					shot.ImageStatus = ShotFailed
				} else {
					shot.VideoStatus = ShotFailed
				}
				shot.Error = err.Error()
				return nil
			})
			s.finishGeneration(id)
			return
		}
		if err := s.generateShots(ctx, rt, gen, id, []int{index}); err != nil {
			_, _ = s.store.Update(id, func(x *Short) error {
				if stage == "image" {
					x.Shots[index].ImageStatus = ShotFailed
				} else {
					x.Shots[index].VideoStatus = ShotFailed
				}
				x.Shots[index].Error = err.Error()
				return nil
			})
			s.fail(id, err)
			return
		}
		s.finishGeneration(id)
	}()
	return nil
}

// RegenerateCharacter 重画某个角色设定图（后续镜头需手动重生才会用新图）。
func (s *Service) RegenerateCharacter(id string, index int) error {
	if !s.tryLock(id) {
		return ErrBusy
	}
	go func() {
		defer s.unlock(id)
		ctx := context.Background()
		_, _ = s.store.Update(id, func(x *Short) error {
			if index < 0 || index >= len(x.Characters) {
				return errors.New("character index out of range")
			}
			x.Characters[index].ImagePath, x.Characters[index].Status, x.Characters[index].Error = "", ShotPending, ""
			return nil
		})
		rt, gen, _, err := s.clients(ctx)
		if err != nil {
			s.fail(id, err)
			return
		}
		if err := s.generateCharacters(ctx, rt, gen, id); err != nil {
			s.fail(id, err)
		}
	}()
	return nil
}

func (s *Service) fail(id string, err error) {
	slog.Default().Warn("ai short failed", "id", id, "error", err)
	_, _ = s.store.Update(id, func(x *Short) error { x.Status, x.Error = StatusFailed, err.Error(); return nil })
}

func (s *Service) finishGeneration(id string) {
	_, _ = s.store.Update(id, func(x *Short) error {
		allDone := len(x.Shots) > 0
		anyRunning := false
		for _, shot := range x.Shots {
			if shot.ImageStatus == ShotRunning || shot.VideoStatus == ShotRunning {
				anyRunning = true
			}
			if !x.ShotReady(shot) {
				allDone = false
			}
		}
		if anyRunning {
			// 其他手动提交的分镜还在跑，保持轮询，不把整条短片提前降回 storyboard。
			x.Status, x.Error = StatusGenerating, ""
		} else if allDone {
			x.Status, x.Error = StatusReady, ""
		} else {
			x.Status = StatusStoryboard
			x.Error = "有镜头没出来，点那张卡重生"
		}
		return nil
	})
}

func imageModelFor(short *Short, rt Runtime) string {
	if model := strings.TrimSpace(short.ImageModel); model != "" {
		return model
	}
	return rt.Models.Image
}

func (s *Service) generateCharacters(ctx context.Context, rt Runtime, gen *GenClient, id string) error {
	short, err := s.store.Get(id)
	if err != nil {
		return err
	}
	dir := s.store.AssetDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrencyOr(rt.ImageConcurrency, 20))
	var firstErr error
	var mu sync.Mutex
	for i, c := range short.Characters {
		if c.Status == ShotDone && c.ImagePath != "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, c Character) {
			defer wg.Done()
			defer func() { <-sem }()
			_, _ = s.store.Update(id, func(x *Short) error { x.Characters[i].Status = ShotRunning; return nil })
			release, err := s.imageGate.acquire(ctx, concurrencyOr(rt.ImageConcurrency, 20))
			var jpeg []byte
			if err == nil {
				jpeg, err = gen.GenerateImage(ctx, imageModelFor(short, rt), characterPrompt(short.Style, c), nil)
				release()
			}
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("角色「%s」设定图失败：%w", c.Name, err)
				}
				mu.Unlock()
				_, _ = s.store.Update(id, func(x *Short) error {
					x.Characters[i].Status, x.Characters[i].Error = ShotFailed, err.Error()
					return nil
				})
				return
			}
			path := filepath.Join(dir, fmt.Sprintf("character_%d.jpg", i))
			if err := os.WriteFile(path, jpeg, 0o644); err != nil {
				return
			}
			_, _ = s.store.Update(id, func(x *Short) error {
				x.Characters[i].ImagePath, x.Characters[i].Status, x.Characters[i].Error = path, ShotDone, ""
				return nil
			})
		}(i, c)
	}
	wg.Wait()
	return firstErr
}

// generateShots 对指定镜头（nil=全部未完成）依次：首帧图 → 图生视频。
func (s *Service) generateShots(ctx context.Context, rt Runtime, gen *GenClient, id string, only []int) error {
	if err := s.prepareOpeningTimeline(ctx, rt, id); err != nil {
		return err
	}
	short, err := s.store.Get(id)
	if err != nil {
		return err
	}
	dir := s.store.AssetDir(id)
	_ = os.MkdirAll(dir, 0o755)
	wanted := map[int]bool{}
	for _, i := range only {
		wanted[i] = true
	}
	// 寓言沿用角色设定图；财经解说只用文字统一画风，避免首图的人物或构图污染后续镜头。
	refs := map[string]string{}
	if !short.IsExplainer() {
		for _, c := range short.Characters {
			if c.ImagePath == "" {
				continue
			}
			if raw, err := os.ReadFile(c.ImagePath); err == nil {
				refs[c.Name] = DataURL(raw)
			}
		}
	}
	var wg sync.WaitGroup
	for i, shot := range short.Shots {
		if only != nil && !wanted[i] {
			continue
		}
		if only == nil && short.ShotReady(shot) {
			continue
		}
		wg.Add(1)
		go func(i int, shot Shot) {
			defer wg.Done()
			s.generateOneShot(ctx, rt, gen, id, dir, i, shot, short, refs, nil)
		}(i, shot)
	}
	wg.Wait()
	return nil
}

// generateShotImage 出一镜的图并写回；失败返回空。
func (s *Service) generateShotImage(ctx context.Context, rt Runtime, gen *GenClient, id, dir string, i int, shot Shot, short *Short, references []string) string {
	setShot := func(mutate func(*Shot)) {
		_, _ = s.store.Update(id, func(x *Short) error {
			if i < len(x.Shots) {
				mutate(&x.Shots[i])
			}
			return nil
		})
	}
	if short.IsExplainer() && strings.TrimSpace(shot.Scene) == "" && strings.TrimSpace(shot.Subject) == "" {
		setShot(func(x *Shot) {
			x.ImageStatus, x.Error = ShotFailed, "配图描述未完成，请先填写本镜主体或画面。"
		})
		return ""
	}
	prompt := shotImagePrompt(short.Style, shot, short.Characters)
	if short.IsExplainer() {
		prompt = explainerImagePrompt(shot)
		if len(references) > 0 {
			prompt += "参考图只用来统一画风、光线、质感和配色，画面内容以上面的文字为准。"
		}
	}
	setShot(func(x *Shot) {
		x.ImageStatus, x.Error, x.ImagePrompt = ShotRunning, "", explainerOrFablePrompt(short, shot)
	})
	release, limitErr := s.imageGate.acquire(ctx, concurrencyOr(rt.ImageConcurrency, 20))
	if limitErr != nil {
		setShot(func(x *Shot) { x.ImageStatus, x.Error = ShotFailed, limitErr.Error() })
		return ""
	}
	defer release()
	size := "1792x1024"
	if short.IsExplainer() && short.VisualSettings != nil && short.VisualSettings.Layout == "portrait_full" {
		size = "1152x2048"
	}
	jpeg, err := gen.GenerateImageSize(ctx, imageModelFor(short, rt), prompt, references, size)
	if err != nil {
		setShot(func(x *Shot) { x.ImageStatus, x.Error = ShotFailed, "生图失败："+err.Error() })
		return ""
	}
	imagePath := filepath.Join(dir, fmt.Sprintf("shot_%02d.jpg", i+1))
	if short.IsExplainer() {
		ext := ".jpg"
		if http.DetectContentType(jpeg) == "image/png" {
			ext = ".png"
		}
		if http.DetectContentType(jpeg) == "image/webp" {
			ext = ".webp"
		}
		imagePath = filepath.Join(dir, fmt.Sprintf("shot_%02d_%s%s", i+1, uuid.NewString(), ext))
	}
	if err := os.WriteFile(imagePath, jpeg, 0o644); err != nil {
		setShot(func(x *Shot) { x.ImageStatus, x.Error = ShotFailed, err.Error() })
		return ""
	}
	setShot(func(x *Shot) {
		x.ImagePath, x.ImageStatus, x.ImagePromptUsed, x.ImageStale = imagePath, ShotDone, prompt, false
	})
	_, _ = s.store.Update(id, func(x *Short) error { markDraftStale(x); return nil })
	return imagePath
}

func (s *Service) generateOneShot(ctx context.Context, rt Runtime, gen *GenClient, id, dir string, i int, shot Shot, short *Short, refs map[string]string, videoSem chan struct{}) {
	setShot := func(mutate func(*Shot)) {
		_, _ = s.store.Update(id, func(x *Short) error {
			if i < len(x.Shots) {
				mutate(&x.Shots[i])
			}
			return nil
		})
	}
	imagePath := shot.ImagePath
	if shot.ImageStatus != ShotDone || imagePath == "" || shot.ImageStale {
		var references []string
		if short.IsExplainer() {
			if ref, ok := refs[shot.StyleKey]; ok {
				references = []string{ref}
			}
		} else {
			for _, name := range shot.Characters {
				if ref, ok := refs[name]; ok {
					references = append(references, ref)
				}
			}
		}
		imagePath = s.generateShotImage(ctx, rt, gen, id, dir, i, shot, short, references)
		if imagePath == "" {
			return
		}
		shot.VideoRequestID = ""
		shot.VideoSubmitUncertain = false
	}
	if !short.NeedsVideo(shot) {
		// 图片镜到此为止：推拉平移在组装时用关键帧做。
		setShot(func(x *Shot) { x.Error = "" })
		return
	}

	// 视频任务单独限流；等待远端完成期间也占用一个视频并发名额。
	release, limitErr := s.videoGate.acquire(ctx, concurrencyOr(rt.VideoConcurrency, 6))
	if limitErr != nil {
		setShot(func(x *Shot) { x.VideoStatus, x.Error = ShotFailed, limitErr.Error() })
		return
	}
	defer release()
	if videoSem != nil {
		videoSem <- struct{}{}
		defer func() { <-videoSem }()
	}
	setShot(func(x *Shot) { x.VideoStatus, x.Error = ShotRunning, "" })
	frame, err := os.ReadFile(imagePath)
	if err != nil {
		setShot(func(x *Shot) { x.VideoStatus, x.Error = ShotFailed, err.Error() })
		return
	}
	videoPrompt := shotVideoPrompt(shot, short.Characters)
	if short.IsExplainer() {
		videoPrompt = explainerVideoPrompt(shot)
	}
	setShot(func(x *Shot) { x.VideoPrompt = videoPrompt })
	gen = videoClient(rt, gen)
	if gen.BaseURL == "" {
		setShot(func(x *Shot) { x.VideoStatus, x.Error = ShotFailed, "请配置视频接口URL" })
		return
	}
	requestID := shot.VideoRequestID
	previousURL := shot.VideoRequestBaseURL
	if previousURL == "" {
		previousURL = rt.BaseURL
	}
	if requestID != "" && previousURL != "" && strings.TrimRight(previousURL, "/") != strings.TrimRight(gen.BaseURL, "/") {
		setShot(func(x *Shot) {
			x.VideoStatus, x.Error = ShotFailed, "这个视频任务属于原接口，请切回原URL继续查询；如需在新接口重新生成，请使用单镜视频按钮。"
		})
		return
	}
	if requestID == "" {
		if shot.VideoSubmitUncertain {
			setShot(func(x *Shot) {
				x.VideoStatus, x.Error = ShotFailed, "上次视频提交结果未确认，已暂停自动重提。请核对API后台后，使用单镜重生视频重新提交。"
			})
			return
		}
		aspect := "16:9"
		if short.IsExplainer() && short.VisualSettings != nil && short.VisualSettings.Layout == "portrait_full" {
			aspect = "9:16"
		}
		// 先落盘再提交，避免进程在收到任务编号前退出后自动重复计费。
		if _, err := s.store.Update(id, func(x *Short) error {
			x.Shots[i].VideoSubmitUncertain = true
			x.Shots[i].VideoRequestID = ""
			return nil
		}); err != nil {
			setShot(func(x *Shot) { x.VideoStatus, x.Error = ShotFailed, err.Error() })
			return
		}
		requestID, err = gen.StartVideoAspect(ctx, videoModelFor(short, rt), videoPrompt, shot.Seconds, DataURL(frame), aspect)
		if err != nil {
			setShot(func(x *Shot) {
				x.VideoStatus, x.Error = ShotFailed, "提交视频失败："+err.Error()
				x.VideoSubmitUncertain = errors.Is(err, ErrVideoSubmissionUncertain)
			})
			return
		}
	}
	if _, err := s.store.Update(id, func(x *Short) error {
		x.Shots[i].VideoRequestID = requestID
		x.Shots[i].VideoRequestBaseURL = gen.BaseURL
		x.Shots[i].VideoSubmitUncertain = false
		return nil
	}); err != nil {
		setShot(func(x *Shot) { x.VideoStatus, x.Error = ShotFailed, "保存视频任务编号失败："+err.Error() })
		return
	}
	mp4, err := gen.WaitVideo(ctx, requestID, 6*time.Minute)
	if err != nil {
		setShot(func(x *Shot) {
			x.VideoStatus, x.Error = ShotFailed, "视频生成失败："+err.Error()
			if errors.Is(err, ErrVideoTaskTerminal) {
				x.VideoRequestID = ""
			}
		})
		return
	}
	videoPath := filepath.Join(dir, fmt.Sprintf("shot_%02d.mp4", i+1))
	if short.IsExplainer() {
		videoPath = filepath.Join(dir, fmt.Sprintf("shot_%02d_%s.mp4", i+1, uuid.NewString()))
	}
	if err := os.WriteFile(videoPath, mp4, 0o644); err != nil {
		setShot(func(x *Shot) { x.VideoStatus, x.Error = ShotFailed, err.Error() })
		return
	}
	setShot(func(x *Shot) { x.VideoPath, x.VideoStatus, x.Error = videoPath, ShotDone, "" })
	_, _ = s.store.Update(id, func(x *Short) error { markDraftStale(x); return nil })
}

// AssembleAsync 后台：配音 → 分镜计时 → 剪映草稿 → 注册进剪映。
func (s *Service) AssembleAsync(id string) error {
	if !s.tryLock(id) {
		return ErrBusy
	}
	short, err := s.store.Get(id)
	if err != nil {
		s.unlock(id)
		return err
	}
	if short.StoryboardStale {
		s.unlock(id)
		return errors.New("文案已更新，请先重新拆分镜")
	}
	for _, shot := range short.Shots {
		if !short.ShotReady(shot) {
			s.unlock(id)
			return fmt.Errorf("第 %d 镜画面还没出来，先把所有镜头出完", shot.Index+1)
		}
	}
	if s.assemble == nil {
		s.unlock(id)
		return errors.New("组装器未配置")
	}
	if _, err := s.store.Update(id, func(x *Short) error {
		x.Status, x.Error = StatusAssembling, ""
		x.AssemblyProgress = &AssemblyProgress{Message: "准备配音与草稿", StartedAt: now(), UpdatedAt: now()}
		return nil
	}); err != nil {
		s.unlock(id)
		return err
	}
	go func() {
		defer s.unlock(id)
		ctx := context.Background()
		rt, err := s.runtime(ctx)
		if err != nil {
			s.fail(id, err)
			return
		}
		progress := func(step string) {
			_, _ = s.store.Update(id, func(x *Short) error { x.AssemblyProgress = assemblyStep(step, x.AssemblyProgress); return nil })
		}
		result, err := s.assemble.Assemble(ctx, rt, short, s.store.AssetDir(id), progress)
		if err != nil {
			s.fail(id, err)
			return
		}
		_, _ = s.store.Update(id, func(x *Short) error {
			x.Status, x.Error = StatusAssembled, ""
			x.AssemblyProgress = &AssemblyProgress{Stage: 4, Message: "草稿已成功导入剪映", StartedAt: x.AssemblyProgress.StartedAt, UpdatedAt: now()}
			x.NarrationPath, x.SRTPath = result.NarrationPath, result.SRTPath
			x.DraftPath, x.DraftName, x.DurationS = result.DraftPath, result.DraftName, result.DurationS
			x.DraftStale, x.Captions = false, result.Captions
			for i := range x.Shots {
				if i < len(result.ShotTimes) {
					x.Shots[i].StartS, x.Shots[i].EndS = result.ShotTimes[i][0], result.ShotTimes[i][1]
				}
			}
			return nil
		})
	}()
	return nil
}

func firstLine(text string, max int) string {
	text = strings.TrimSpace(text)
	if cut := strings.IndexAny(text, "。！？\n"); cut > 0 {
		text = text[:cut]
	}
	runes := []rune(text)
	if len(runes) > max {
		return string(runes[:max])
	}
	return text
}
