package refanalysis

import (
	"encoding/json"
	"errors"
	"strings"
)

type Report struct {
	Summary  string `json:"summary"`
	Audience string `json:"audience"`
	Hook     struct {
		Type       string `json:"type"`
		First3S    string `json:"first_3s"`
		WhyItWorks string `json:"why_it_works"`
	} `json:"hook"`
	Structure []struct {
		StartS      float64 `json:"start_s"`
		EndS        float64 `json:"end_s"`
		Beat        string  `json:"beat"`
		WhatHappens string  `json:"what_happens"`
		Purpose     string  `json:"purpose"`
	} `json:"structure"`
	Visual struct {
		ShotTypes    string `json:"shot_types"`
		CameraMoves  string `json:"camera_moves"`
		BRoll        string `json:"b_roll"`
		OnScreenText string `json:"on_screen_text"`
		Pace         string `json:"pace"`
	} `json:"visual"`
	Audio struct {
		Voice string `json:"voice"`
		Music string `json:"music"`
		SFX   string `json:"sfx"`
	} `json:"audio"`
	Persuasion []string `json:"persuasion"`
	Conversion string   `json:"conversion"`
	Replicate  struct {
		StyleLock string `json:"style_lock"`
		AIPrompts []struct {
			ShotIndex        int     `json:"shot_index"`
			StartS           float64 `json:"start_s"`
			EndS             float64 `json:"end_s"`
			Purpose          string  `json:"purpose"`
			FirstFramePrompt string  `json:"first_frame_prompt"`
			MotionPrompt     string  `json:"motion_prompt"`
			T2VPrompt        string  `json:"t2v_prompt"`
			Negative         string  `json:"negative"`
			Keep             string  `json:"keep"`
		} `json:"ai_prompts"`
		ShotList []struct {
			Shot       string  `json:"shot"`
			DurationS  float64 `json:"duration_s"`
			HowToShoot string  `json:"how_to_shoot"`
			Line       string  `json:"line"`
		} `json:"shot_list"`
		ScriptSkeleton []string `json:"script_skeleton"`
		EditingTips    []string `json:"editing_tips"`
		Variables      []string `json:"variables"`
		Avoid          []string `json:"avoid"`
	} `json:"replicate"`
	Difficulty string `json:"difficulty"`
}

// ParseReport 接受缺省字段；围栏或说明文字只用于定位首个完整 JSON 对象。
func ParseReport(content string) (Report, error) {
	content = strings.TrimSpace(content)
	raw := []byte(content)
	if !json.Valid(raw) {
		raw = nil
		for i := 0; i < len(content); i++ {
			if content[i] != '{' {
				continue
			}
			var candidate json.RawMessage
			if err := json.NewDecoder(strings.NewReader(content[i:])).Decode(&candidate); err == nil {
				raw = candidate
				break
			}
		}
	}
	if len(raw) == 0 || raw[0] != '{' {
		return Report{}, errors.New("模型没有返回有效的 JSON 报告")
	}
	var report Report
	if err := json.Unmarshal(raw, &report); err != nil {
		return Report{}, errors.New("模型报告字段类型不正确")
	}
	return report, nil
}
