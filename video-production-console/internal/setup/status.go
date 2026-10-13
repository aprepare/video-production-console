package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"video-production-console/internal/domain"
	"video-production-console/internal/jianyingdraft"
	"video-production-console/internal/platformapi"
	"video-production-console/internal/settings"
)

type Item struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Required bool   `json:"required"`
	Status   string `json:"status"`
	Detail   string `json:"detail"`
	Fix      string `json:"fix"`
}

type Status struct {
	Ready bool   `json:"ready"`
	Items []Item `json:"items"`
}

type checkResult struct{ status, detail, fix string }
type check struct {
	item Item
	run  func(context.Context) checkResult
}

// Status 的全部检查共用一个截止时间，慢网络或慢磁盘不会串行累加等待。
func (s *Service) Status(ctx context.Context) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	view, err := s.settings.Get(ctx)
	if err != nil {
		return Status{}, errors.New("配置读取失败，请稍后重试。")
	}
	p := view.Public
	var profile machineProfile
	var profileErr error
	var once sync.Once
	loadProfile := func() (machineProfile, error) {
		once.Do(func() { profile, profileErr = readMachineProfile(p.MachineProfilePath) })
		return profile, profileErr
	}
	checks := []check{
		{Item{ID: "writer_model", Title: "二创写作模型", Required: true}, func(ctx context.Context) checkResult {
			return s.modelCheck(ctx, p.RemixBaseURL, p.RemixModel, settings.SecretRemixAPIKey, true)
		}},
		{Item{ID: "review_model", Title: "审稿模型"}, func(context.Context) checkResult {
			if strings.TrimSpace(p.RemixCheckModel) == "" {
				return checkResult{"warn", "未单独设置，审稿会用写作模型；如需独立审稿模型，请在二创设置中选择。", "settings:remix"}
			}
			return checkResult{"ok", "已设置独立审稿模型，可以继续制作。", "none"}
		}},
		{Item{ID: "search_model", Title: "联网搜索模型"}, func(ctx context.Context) checkResult {
			return s.modelCheck(ctx, p.GrokBaseURL, p.GrokModel, settings.SecretGrokAPIKey, false)
		}},
		{Item{ID: "image_model", Title: "生图模型"}, func(ctx context.Context) checkResult {
			return s.configuredService(ctx, settings.SecretImageAPIKey, p.ImageBaseURL, p.ImageModel)
		}},
		{Item{ID: "platform_api", Title: "平台解析接口", Required: true}, func(ctx context.Context) checkResult { return s.platformCheck(ctx, p.PlatformAPIBaseURL) }},
		{Item{ID: "tts", Title: "配音服务", Required: true}, func(ctx context.Context) checkResult { return s.voiceCheck(ctx, p) }},
		{Item{ID: "jianying", Title: "剪映草稿目录", Required: true}, func(context.Context) checkResult { return jianyingCheck(p.JianyingRoot) }},
		{Item{ID: "ffmpeg", Title: "视频处理工具", Required: true}, s.ffmpegCheck},
		{Item{ID: "python", Title: "Python 运行时", Required: true}, func(context.Context) checkResult {
			profile, err := loadProfile()
			if err != nil {
				return profileFailure(err, false)
			}
			return fileCheck(profile.PythonBinary, "Python 运行时", "请在系统设置中检查机器档案或重新安装内置运行时。")
		}},
		{Item{ID: "jianying_assets", Title: "剪映音效与转场缓存"}, func(context.Context) checkResult {
			profile, err := loadProfile()
			if err != nil {
				return profileFailure(err, true)
			}
			if len(profile.CachePaths) == 0 {
				return checkResult{"warn", "尚未登记混剪音效和转场缓存，请在剪映中下载素材并更新机器档案。", "none"}
			}
			missing := 0
			for _, path := range profile.CachePaths {
				if !regularFile(path) {
					missing++
				}
			}
			if missing > 0 {
				return checkResult{"warn", fmt.Sprintf("%d 个混剪音效/转场缓存缺失，换电脑后需要在剪映里重新下载。", missing), "none"}
			}
			return checkResult{"ok", "已登记的剪映缓存齐全，可以继续混剪。", "none"}
		}},
		{Item{ID: "media_library", Title: "本地素材库"}, func(context.Context) checkResult {
			root := p.MediaRoot
			if strings.TrimSpace(root) == "" {
				profile, err := loadProfile()
				if err != nil {
					return profileFailure(err, false)
				}
				root = profile.MediaRoot
			}
			if strings.TrimSpace(root) == "" {
				return checkResult{"missing", "尚未配置素材目录，请在系统设置中选择本地素材库。", "settings:system"}
			}
			if info, err := os.Stat(root); err != nil || !info.IsDir() {
				return checkResult{"error", "素材目录不存在或无法读取，请在系统设置中重新选择目录。", "settings:system"}
			}
			return checkResult{"ok", "素材目录可用，可以导入素材并继续制作。", "none"}
		}},
	}
	result := Status{Ready: true, Items: make([]Item, len(checks))}
	type completed struct {
		index  int
		result checkResult
	}
	finished := make(chan completed, len(checks))
	for i, c := range checks {
		result.Items[i] = c.item
		result.Items[i].Status = "error"
		result.Items[i].Detail = "检查超时或已取消，请检查本机环境和网络后重试。"
		result.Items[i].Fix = "settings:system"
		go func() { finished <- completed{i, c.run(ctx)} }()
	}
	for pending := len(checks); pending > 0; pending-- {
		select {
		case done := <-finished:
			item := &result.Items[done.index]
			item.Status, item.Detail, item.Fix = done.result.status, done.result.detail, done.result.fix
		case <-ctx.Done():
			pending = 1
		}
	}
	for _, item := range result.Items {
		if item.Required && item.Status != "ok" {
			result.Ready = false
		}
	}
	return result, nil
}

func (s *Service) configuredService(ctx context.Context, secret string, fields ...string) checkResult {
	key, err := s.settings.ReadSecret(ctx, secret)
	if err != nil {
		return checkResult{"error", "已保存的密钥无法读取，请重新粘贴配置码或填写密钥。", "activation_code"}
	}
	for _, field := range append(fields, key) {
		if strings.TrimSpace(field) == "" {
			return checkResult{"missing", "服务地址、模型、音色或密钥未配置完整，请粘贴服务商提供的配置码或补全对应设置。", "activation_code"}
		}
	}
	return checkResult{"ok", "配置已齐全，可以继续制作；使用时将连接对应服务。", "none"}
}

func (s *Service) modelCheck(ctx context.Context, base, model, secret string, requireListed bool) checkResult {
	configured := s.configuredService(ctx, secret, base, model)
	if configured.status != "ok" {
		return configured
	}
	key, err := s.settings.ReadSecret(ctx, secret)
	if err != nil {
		return checkResult{"error", "密钥读取失败，请重新粘贴配置码。", "activation_code"}
	}
	models, err := s.requestModels(ctx, base, key)
	if err != nil {
		return checkResult{"error", err.Error(), "settings:remix"}
	}
	if requireListed {
		found := false
		for _, id := range models {
			if id == strings.TrimSpace(model) {
				found = true
				break
			}
		}
		if !found {
			return checkResult{"warn", "服务可连接，但列表中没有当前写作模型；部分网关不会列全，请核对模型名或联系服务商确认。", "settings:remix"}
		}
	}
	return checkResult{"ok", "模型服务可连接，可以继续制作。", "none"}
}

func (s *Service) platformCheck(ctx context.Context, base string) checkResult {
	configured := s.configuredService(ctx, settings.SecretPlatformAPIToken, base)
	if configured.status != "ok" {
		return configured
	}
	key, err := s.settings.ReadSecret(ctx, settings.SecretPlatformAPIToken)
	if err != nil {
		return checkResult{"error", "平台密钥读取失败，请重新粘贴配置码。", "activation_code"}
	}
	var payload struct {
		OK   *bool                   `json:"ok"`
		Apps []platformapi.AppStatus `json:"apps"`
	}
	if err := s.requestJSON(ctx, base, "/api/status", key, &payload); err != nil {
		return checkResult{"error", err.Error(), "activation_code"}
	}
	if payload.OK != nil && !*payload.OK {
		return checkResult{"error", "平台服务报告异常，请联系服务商恢复接口。", "activation_code"}
	}
	names := map[string]string{"douyin": "抖音", "xhs": "小红书", "xiaohongshu": "小红书", "channels": "视频号", "weixin": "视频号", "wechat": "视频号", "tingwu": "通义听悟"}
	var parts []string
	needsHelp := false
	for _, app := range payload.Apps {
		name := names[app.ID]
		if name == "" {
			continue
		}
		// need_you 只是网关提醒服务商 Cookie/dump 文件放久了，平台仍可用；
		// 客户处理不了，这里只看 ready。
		state := "可用"
		if !app.Ready {
			state = "需维护"
			needsHelp = true
		}
		parts = append(parts, name+" "+state)
	}
	if len(parts) == 0 {
		return checkResult{"ok", "平台接口可连接，未提供各平台状态；使用前请联系服务商确认目标平台可用。", "none"}
	}
	next := "；可以使用已就绪的平台。"
	if needsHelp {
		next = "；需维护的平台请联系服务商处理，其余平台可继续使用。"
	}
	return checkResult{"ok", strings.Join(parts, " / ") + next, "none"}
}

func (s *Service) voiceCheck(ctx context.Context, p domain.PublicSettings) checkResult {
	switch p.TTSProvider {
	case "aurastd":
		return s.configuredService(ctx, settings.SecretAuraSTDTTsAPIKey, p.AuraSTDVoiceID)
	case "volc":
		return s.configuredService(ctx, settings.SecretVolcSpeechAPIKey, p.VolcSpeechSpeakerID)
	case "":
		return checkResult{"missing", "尚未选择配音服务，请粘贴配置码或在设置中选择接口和音色。", "activation_code"}
	default:
		return checkResult{"error", "当前配音服务不受支持，请在设置中重新选择接口。", "settings:system"}
	}
}

func jianyingCheck(root string) checkResult {
	if strings.TrimSpace(root) == "" {
		return checkResult{"missing", "尚未配置剪映草稿目录，请打开剪映新建并关闭一个空草稿后再检测。", "jianying_detect"}
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() || !regularFile(filepath.Join(root, "root_meta_info.json")) {
		return checkResult{"error", "剪映草稿目录或注册文件不存在，请打开剪映新建并关闭一个空草稿后再检测。", "jianying_detect"}
	}
	installed, err := jianyingdraft.DetectInstalledVersion(root)
	if err != nil {
		return checkResult{"error", "剪映版本读取失败，请确认草稿目录可读后重新检测。", "jianying_detect"}
	}
	if !jianyingdraft.CompatibleWithVerified(installed.Version, jianyingdraft.VerifiedJianyingVersions) {
		return checkResult{"warn", "当前剪映版本未经过兼容验证，请使用已验证版本或先制作一个测试草稿。", "jianying_detect"}
	}
	return checkResult{"ok", "剪映目录与版本已就绪，可以继续创建草稿。", "none"}
}

func (s *Service) ffmpegCheck(ctx context.Context) checkResult {
	runtime, err := s.settings.Runtime(ctx)
	if err != nil {
		return checkResult{"error", "运行配置无法读取，请检查系统设置后重启控制台。", "settings:system"}
	}
	for _, tool := range []struct{ path, title string }{{runtime.FFmpegPath, "FFmpeg"}, {runtime.FFprobePath, "FFprobe"}} {
		result := fileCheck(tool.path, tool.title, "请重新安装内置视频工具，或在系统设置中选择可执行文件后重启。")
		if result.status != "ok" {
			return result
		}
	}
	return checkResult{"ok", "视频处理工具已就绪，可以继续制作。", "none"}
}

func fileCheck(path, title, next string) checkResult {
	if strings.TrimSpace(path) == "" {
		return checkResult{"missing", title + "尚未配置；" + next, "settings:system"}
	}
	if !regularFile(path) {
		return checkResult{"error", title + "文件不存在或无法读取；" + next, "settings:system"}
	}
	return checkResult{"ok", title + "已就绪，可以继续制作。", "none"}
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

type machineProfile struct {
	PythonBinary string            `json:"python_binary"`
	MediaRoot    string            `json:"media_root"`
	CachePaths   map[string]string `json:"cache_paths"`
}

func readMachineProfile(path string) (machineProfile, error) {
	if strings.TrimSpace(path) == "" {
		return machineProfile{}, os.ErrNotExist
	}
	f, err := os.Open(path)
	if err != nil {
		return machineProfile{}, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if err != nil {
		return machineProfile{}, err
	}
	var profile machineProfile
	if len(raw) > 4<<20 || json.Unmarshal(raw, &profile) != nil || strings.TrimSpace(string(raw)) == "null" {
		return machineProfile{}, errors.New("机器档案格式无效")
	}
	return profile, nil
}

func profileFailure(err error, optional bool) checkResult {
	state := "error"
	if errors.Is(err, os.ErrNotExist) {
		state = "missing"
		if optional {
			state = "warn"
		}
	}
	return checkResult{state, "机器档案缺失或无法读取，请在系统设置中选择本机档案或重新安装运行环境。", "settings:system"}
}
