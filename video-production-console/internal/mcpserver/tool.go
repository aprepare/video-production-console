package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Param 描述工具的一个参数。In 决定它放在哪：path（路径里的 {name}）、query、body（默认）。
type Param struct {
	Type        string // string / integer / number / boolean / array / object
	Description string
	Required    bool
	In          string
	Enum        []string
	Items       string // array 元素类型
	// BodyName 让工具参数名和接口字段名不同时做映射；空则同名。
	BodyName string
}

// Tool 是一个 MCP 工具。普通工具把参数拼成一次 HTTP 请求；Run 非空时自定义执行。
type Tool struct {
	Name        string
	Description string
	Method      string
	Path        string // 可含 {id} 这类占位
	Params      map[string]Param
	// Confirm 非空表示花钱、删除或不可逆：AI 必须先问用户，再带 confirm=true 调用。
	Confirm string
	// BodyArg 非空时把这个参数的值整个作为请求体（用于接口直接收一个大对象的场景）。
	BodyArg string
	Run     func(context.Context, *Server, map[string]any) toolResult
}

var placeholder = regexp.MustCompile(`\{([a-zA-Z_]+)\}`)

func (t Tool) descriptor() map[string]any {
	props := map[string]any{}
	var required []string
	for _, name := range sortedKeys(t.Params) {
		p := t.Params[name]
		schema := map[string]any{"type": p.Type}
		if p.Description != "" {
			schema["description"] = p.Description
		}
		if len(p.Enum) > 0 {
			schema["enum"] = p.Enum
		}
		if p.Type == "array" {
			item := p.Items
			if item == "" {
				item = "string"
			}
			schema["items"] = map[string]any{"type": item}
		}
		props[name] = schema
		if p.Required {
			required = append(required, name)
		}
	}
	description := t.Description
	if t.Confirm != "" {
		props["confirm"] = map[string]any{"type": "boolean", "description": "用户已明确同意后才传 true"}
		description += "\n⚠ 需要用户确认：" + t.Confirm + "。先问用户，同意后带 confirm=true。"
	}
	if t.Method != "" && t.Run == nil {
		description += fmt.Sprintf("\n（接口 %s %s）", t.Method, t.Path)
	}
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return map[string]any{"name": t.Name, "description": description, "inputSchema": schema}
}

// request 把参数拼成 HTTP 请求。
func (t Tool) request(args map[string]any) (string, string, url.Values, any, error) {
	for name, p := range t.Params {
		if p.Required {
			if v, ok := args[name]; !ok || v == nil || v == "" {
				return "", "", nil, nil, fmt.Errorf("缺少必填参数 %s：%s", name, p.Description)
			}
		}
	}
	path := t.Path
	for _, match := range placeholder.FindAllStringSubmatch(t.Path, -1) {
		value := fmt.Sprint(args[match[1]])
		if strings.TrimSpace(value) == "" || value == "<nil>" {
			return "", "", nil, nil, fmt.Errorf("缺少路径参数 %s", match[1])
		}
		path = strings.ReplaceAll(path, match[0], url.PathEscape(value))
	}
	query := url.Values{}
	body := map[string]any{}
	for name, value := range args {
		if name == "confirm" {
			continue
		}
		p, known := t.Params[name]
		if !known {
			// 接口按 DisallowUnknownFields 解码，多一个字段就 400：直接告诉 AI 哪个参数不认识。
			return "", "", nil, nil, fmt.Errorf("工具 %s 没有参数 %s；需要别的字段请用 console_api", t.Name, name)
		}
		switch p.In {
		case "path":
		case "query":
			if value != nil && fmt.Sprint(value) != "" {
				query.Set(name, fmt.Sprint(value))
			}
		default:
			key := name
			if p.BodyName != "" {
				key = p.BodyName
			}
			body[key] = value
		}
	}
	if t.BodyArg != "" {
		return t.Method, path, query, args[t.BodyArg], nil
	}
	if t.Method == http.MethodGet || (t.Method == http.MethodDelete && len(body) == 0) {
		return t.Method, path, query, nil, nil
	}
	return t.Method, path, query, body, nil
}

// genericTools 兜底：任何没有单独列出的操作都能通过 console_api 完成。
func genericTools() []Tool {
	return []Tool{
		{
			Name: "console_api",
			Description: "直接调用控制台任意 /api 接口（专用工具没覆盖到的操作用它）。先用 console_api_catalog 查接口。" +
				"删除、花钱（调用模型、配音、生图、RunningHub）、改设置这类操作，必须先征得用户同意再调用。",
			Params: map[string]Param{
				"method": {Type: "string", Required: true, Enum: []string{"GET", "POST", "PUT", "PATCH", "DELETE"}, Description: "HTTP 方法"},
				"path":   {Type: "string", Required: true, Description: "以 /api/ 开头的路径，可带 ?query"},
				"body":   {Type: "object", Description: "JSON 请求体（GET 不传）"},
			},
			Run: func(ctx context.Context, s *Server, args map[string]any) toolResult {
				method := strings.ToUpper(fmt.Sprint(args["method"]))
				raw := fmt.Sprint(args["path"])
				parsed, err := url.Parse(raw)
				if err != nil {
					return textResult("路径无效："+err.Error(), true)
				}
				return s.do(ctx, method, parsed.Path, parsed.Query(), args["body"])
			},
		},
		{
			Name:        "console_api_catalog",
			Description: "列出控制台全部 /api 接口（方法、路径、用途、是否花钱/删除），配合 console_api 使用。可用 keyword 过滤。",
			Params: map[string]Param{
				"keyword": {Type: "string", Description: "按路径或说明过滤，例如 merchants、封面、提示词"},
			},
			Run: func(_ context.Context, _ *Server, args map[string]any) toolResult {
				keyword := strings.TrimSpace(fmt.Sprint(args["keyword"]))
				if keyword == "<nil>" {
					keyword = ""
				}
				var b strings.Builder
				for _, line := range strings.Split(APICatalog, "\n") {
					if keyword == "" || strings.Contains(strings.ToLower(line), strings.ToLower(keyword)) {
						b.WriteString(line)
						b.WriteByte('\n')
					}
				}
				if b.Len() == 0 {
					return textResult("没有匹配的接口", false)
				}
				return textResult(b.String(), false)
			},
		},
	}
}
