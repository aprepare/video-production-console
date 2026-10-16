package runninghub

import (
	"errors"
	"strconv"
	"strings"
)

const H3WebappID = "2084320751339032577"

type Node struct {
	NodeID        string `json:"nodeId"`
	NodeName      string `json:"nodeName"`
	FieldName     string `json:"fieldName"`
	FieldValue    string `json:"fieldValue"`
	FieldData     string `json:"fieldData"`
	FieldType     string `json:"fieldType"`
	Description   string `json:"description"`
	DescriptionCn any    `json:"descriptionCn"`
	DescriptionEn any    `json:"descriptionEn"`
}

type H3Request struct {
	Image          []byte
	ImageName      string
	Prompt         string
	DurationS      int
	Ratio          string
	PromptOptimize bool
}

var h3Template = []Node{
	{NodeID: "141", NodeName: "LoadImage", FieldName: "image", FieldValue: "None", FieldData: `[["example.png", "None", "keep_this_dic"], {"image_upload": true}]`, FieldType: "IMAGE", Description: "参考图1"},
	{NodeID: "142", NodeName: "LoadImage", FieldName: "image", FieldValue: "None", FieldData: `[["example.png", "None", "keep_this_dic"], {"image_upload": true}]`, FieldType: "IMAGE", Description: "参考图2"},
	{NodeID: "143", NodeName: "LoadImage", FieldName: "image", FieldValue: "None", FieldData: `[["example.png", "None", "keep_this_dic"], {"image_upload": true}]`, FieldType: "IMAGE", Description: "参考图3"},
	{NodeID: "171", NodeName: "PrimitiveBoolean", FieldName: "value", FieldValue: "false", FieldData: `["BOOLEAN", {}]`, FieldType: "BOOLEAN", Description: "最高画质参考（一致性最强）"},
	{NodeID: "144", NodeName: "LoadAudio", FieldName: "audio", FieldValue: "None", FieldData: `["COMBO", {"options": ["None"], "multiselect": false, "audio_upload": true}]`, FieldType: "AUDIO", Description: "参考音频1"},
	{NodeID: "164", NodeName: "VHS_LoadVideo", FieldName: "video", FieldValue: "None", FieldData: `["STRING", {"default": "None", "vhs_path_extensions": ["webm", "mp4", "mkv", "gif", "mov"]}]`, FieldType: "VIDEO", Description: "参考视频（勿删，打开开关才生效）"},
	{NodeID: "163", NodeName: "PrimitiveBoolean", FieldName: "value", FieldValue: "false", FieldData: `["BOOLEAN", {}]`, FieldType: "BOOLEAN", Description: "参考视频开关"},
	{NodeID: "150", NodeName: "PrimitiveStringMultiline", FieldName: "value", FieldValue: "", FieldData: `["STRING", {"multiline": true}]`, FieldType: "STRING", Description: "提示词"},
	{NodeID: "185", NodeName: "PrimitiveBoolean", FieldName: "value", FieldValue: "true", FieldData: `["BOOLEAN", {}]`, FieldType: "BOOLEAN", Description: "提示词优化"},
	{NodeID: "115", NodeName: "ResolutionSelector", FieldName: "aspect_ratio", FieldValue: "16:9 (Widescreen)", FieldData: `["COMBO", {"default": "1:1 (Square)", "options": ["1:1 (Square)", "2:3 (Portrait Photo)", "3:2 (Photo)", "3:4 (Portrait Standard)", "4:3 (Standard)", "9:16 (Portrait Widescreen)", "16:9 (Widescreen)", "21:9 (Ultrawide)"], "tooltip": "The aspect ratio for the output dimensions.", "multiselect": false}]`, FieldType: "LIST", Description: "比例选择"},
	{NodeID: "186", NodeName: "Int", FieldName: "value", FieldValue: "5", FieldData: `["INT", {"default": 0}]`, FieldType: "INT", Description: "时长/秒"},
	{NodeID: "147", NodeName: "PrimitiveFloat", FieldName: "value", FieldValue: "0.5", FieldData: `["FLOAT", {"max": 9223372036854775807, "min": -9223372036854775807, "step": 0.1}]`, FieldType: "FLOAT", Description: "像素/百万"},
	{NodeID: "161", NodeName: "LoadImage", FieldName: "image", FieldValue: "None", FieldData: `[["example.png", "None", "keep_this_dic"], {"image_upload": true}]`, FieldType: "IMAGE", Description: "参考图4"},
	{NodeID: "175", NodeName: "LoadImage", FieldName: "image", FieldValue: "None", FieldData: `[["example.png", "None", "keep_this_dic"], {"image_upload": true}]`, FieldType: "IMAGE", Description: "参考图5"},
	{NodeID: "176", NodeName: "LoadImage", FieldName: "image", FieldValue: "None", FieldData: `[["example.png", "None", "keep_this_dic"], {"image_upload": true}]`, FieldType: "IMAGE", Description: "参考图6"},
	{NodeID: "160", NodeName: "LoadAudio", FieldName: "audio", FieldValue: "None", FieldData: `["COMBO", {"options": ["None"], "multiselect": false, "audio_upload": true}]`, FieldType: "AUDIO", Description: "参考音频2"},
	{NodeID: "189", NodeName: "LoadAudio", FieldName: "audio", FieldValue: "None", FieldData: `["COMBO", {"options": ["None"], "multiselect": false, "audio_upload": true}]`, FieldType: "AUDIO", Description: "参考音频3"},
	{NodeID: "192", NodeName: "Int", FieldName: "value", FieldValue: "8", FieldData: `["INT", {"default": 0}]`, FieldType: "INT", Description: "采样步数"},
	{NodeID: "159", NodeName: "LoraLoaderModelOnly", FieldName: "lora_name", FieldValue: "MysticXXX_MMH3-V1.safetensors", FieldData: `[[]]`, FieldType: "LORA", Description: "解剖结构专用lora"},
	{NodeID: "159", NodeName: "LoraLoaderModelOnly", FieldName: "strength_model", FieldValue: "0", FieldData: `["FLOAT", {"max": 100, "min": -100, "step": 0.01, "default": 1}]`, FieldType: "FLOAT", Description: "lora强度"},
	{NodeID: "169", NodeName: "PrimitiveBoolean", FieldName: "value", FieldValue: "false", FieldData: `["BOOLEAN", {}]`, FieldType: "BOOLEAN", Description: "补帧开关"},
	{NodeID: "158", NodeName: "PrimitiveBoolean", FieldName: "value", FieldValue: "false", FieldData: `["BOOLEAN", {}]`, FieldType: "BOOLEAN", Description: "仅Zip输出"},
}

func BuildH3Nodes(request H3Request, uploadedName string) ([]Node, error) {
	if strings.TrimSpace(uploadedName) == "" || strings.TrimSpace(request.Prompt) == "" || request.DurationS < 1 || request.DurationS > 10 {
		return nil, errors.New("invalid H3 request")
	}
	ratio, ok := map[string]string{"1:1": "1:1 (Square)", "3:4": "3:4 (Portrait Standard)", "4:3": "4:3 (Standard)", "9:16": "9:16 (Portrait Widescreen)", "16:9": "16:9 (Widescreen)"}[request.Ratio]
	if !ok {
		return nil, errors.New("invalid H3 aspect ratio")
	}
	nodes := append([]Node(nil), h3Template...)
	updates := map[string]string{"141/image": uploadedName, "150/value": request.Prompt, "186/value": strconv.Itoa(request.DurationS), "115/aspect_ratio": ratio, "185/value": strconv.FormatBool(request.PromptOptimize)}
	for index := range nodes {
		if value, found := updates[nodes[index].NodeID+"/"+nodes[index].FieldName]; found {
			nodes[index].FieldValue = value
		}
	}
	return nodes, nil
}
