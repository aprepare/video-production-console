package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"video-production-console/internal/domain"
	"video-production-console/internal/security"
)

var publicJSONKeys = publicSettingJSONKeys()

var publicFieldLabels = map[string]string{
	"public":                         "设置内容",
	"listen_addr":                    "监听地址",
	"data_root":                      "数据目录",
	"max_codex_concurrency":          "同时运行任务数",
	"codex_default_model":            "默认模型",
	"codex_default_reasoning_effort": "默认推理强度",
	"remix_base_url":                 "二创服务地址",
	"remix_model":                    "二创模型",
	"remix_reasoning_effort":         "二创思考强度",
	"remix_check_model":              "质检模型",
	"spoken_lines_model":             "口播稿模型",
	"remix_prompt_style":             "二创提示词",
	"copy_base_url":                  "口播copy接口",
	"model_options":                  "可选模型列表",
	"image_base_url":                 "生图服务地址",
	"image_model":                    "生图模型",
	"image_text_base_url":            "图文文本模型地址",
	"image_text_model":               "图文文本模型",
	"image_text_reasoning_effort":    "图文文本思考强度",
	"max_image_concurrency":          "同时生成图片数",
	"image_generation_attempts":      "每张图片最多请求次数",
	"default_image_ratio":            "默认图片比例",
	"default_image_style":            "默认视觉风格",
	"codex_binary_path":              "Codex 程序路径",
	"media_index_path":               "素材索引",
	"media_root":                     "媒体素材目录",
	"jianying_root":                  "剪映草稿目录",
	"machine_profile_path":           "混剪机器配置",
	"app_server_enabled":             "任务实时交互服务",
	"codex_workspace_roots":          "Codex 工作目录白名单",
	"codex_task_project_root":        "Codex 任务项目目录",
	"volc_speech_speaker_id":         "火山音色 ID",
	"volc_speech_resource_id":        "火山语音资源 ID",
	"tts_provider":                   "配音接口",
	"aurastd_base_url":               "Aura Studio 地址",
	"aurastd_model":                  "配音模型",
	"aurastd_voice_id":               "克隆音色 ID",
	"aurastd_speed":                  "语速",
	"aurastd_volume":                 "音量",
	"aurastd_pitch":                  "音调",
	"aurastd_emotion":                "情感",
	"aurastd_language_boost":         "语言增强",
	"aurastd_modify_pitch":           "音高",
	"aurastd_modify_intensity":       "强度",
	"aurastd_modify_timbre":          "音色",
	"aurastd_sound_effects":          "音效",
	"media_catalog_path":             "素材库目录",
	"ffmpeg_path":                    "FFmpeg 路径",
	"ffprobe_path":                   "FFprobe 路径",
	"bgm_dir":                        "BGM 目录",
	"montage_style":                  "混剪样式",
}

func publicSettingJSONKeys() map[string]struct{} {
	keys := make(map[string]struct{})
	typ := reflect.TypeOf(domain.PublicSettings{})
	for i := 0; i < typ.NumField(); i++ {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			keys[name] = struct{}{}
		}
	}
	return keys
}

// OverlayPublic copies known keys from raw onto base and ignores unknown keys
// so a hidden or newly added field cannot blank-fail a save.
func OverlayPublic(base domain.PublicSettings, raw json.RawMessage) (domain.PublicSettings, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return base, nil
	}
	var incoming map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &incoming); err != nil {
		return domain.PublicSettings{}, fmt.Errorf("%w: public", ErrInvalidSettings)
	}
	buf, err := json.Marshal(base)
	if err != nil {
		return domain.PublicSettings{}, fmt.Errorf("%w: public", ErrInvalidSettings)
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(buf, &merged); err != nil {
		return domain.PublicSettings{}, fmt.Errorf("%w: public", ErrInvalidSettings)
	}
	for key, value := range incoming {
		if _, ok := publicJSONKeys[key]; !ok {
			continue
		}
		merged[key] = value
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return domain.PublicSettings{}, fmt.Errorf("%w: public", ErrInvalidSettings)
	}
	var result domain.PublicSettings
	if err := json.Unmarshal(out, &result); err != nil {
		return domain.PublicSettings{}, fmt.Errorf("%w: public", ErrInvalidSettings)
	}
	return result, nil
}

func UserMessage(err error) string {
	switch {
	case err == nil:
		return "设置保存失败，请检查填写内容。"
	case errors.Is(err, ErrUnknownSecret):
		return "无法保存：存在不支持的密钥项，请刷新页面后重试。"
	case errors.Is(err, security.ErrSecretTooLarge):
		return "无法保存：密钥过长。"
	case errors.Is(err, security.ErrSecretEmpty):
		return "无法保存：密钥不能为空。"
	case errors.Is(err, ErrInvalidSettings):
		field := strings.TrimPrefix(err.Error(), ErrInvalidSettings.Error()+": ")
		if label, ok := publicFieldLabels[field]; ok {
			return "无法保存：" + label + "填写不正确。"
		}
		return "无法保存：请检查填写内容。"
	default:
		return "设置保存失败，请稍后重试。"
	}
}
