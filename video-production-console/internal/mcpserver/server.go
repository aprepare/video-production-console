// Package mcpserver 把控制台的 HTTP 接口包装成 MCP 工具，让本机的 AI（Claude Code、Codex 等）
// 通过 Streamable HTTP（单请求单 JSON 响应）直接操作控制台。工具不重写业务：每次调用都在进程内
// 转成一次对自身 /api 的请求，沿用全部校验与业务逻辑。
package mcpserver

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	ProtocolVersion = "2024-11-05"
	maxRequestBytes = 4 << 20
	maxResultBytes  = 200 << 10
)

// Server 处理 POST /mcp。
type Server struct {
	api           http.Handler // 已经过鉴权分流的整站 handler；工具调用带内部令牌走旁路
	internalToken string
	token         func() string // 当前 MCP 令牌；读文件以便重置后立即生效
	tools         []Tool
	byName        map[string]Tool
	version       string
}

// New 创建 MCP 服务。api 是控制台整站 handler，internalToken 是进程内回环令牌。
func New(api http.Handler, internalToken string, token func() string, version string) *Server {
	s := &Server{api: api, internalToken: internalToken, token: token, version: version}
	s.tools = append(Catalog(), genericTools()...)
	s.byName = make(map[string]Tool, len(s.tools))
	for _, tool := range s.tools {
		if _, dup := s.byName[tool.Name]; dup {
			panic("duplicate MCP tool " + tool.Name)
		}
		s.byName[tool.Name] = tool
	}
	return s
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !loopback(r.RemoteAddr) {
		http.Error(w, "MCP 只允许本机连接", http.StatusForbidden)
		return
	}
	// 浏览器网页不能借本机身份调 MCP：带 Origin 的跨站请求一律拒绝。
	if origin := r.Header.Get("Origin"); origin != "" && !loopbackOrigin(origin) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	if !s.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="video-production-console"`)
		http.Error(w, "需要 MCP 令牌：在控制台「设置 → AI 接入」复制", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodPost:
	case http.MethodGet:
		// 不提供服务端推送流。
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	case http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
		return
	default:
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
	if err != nil || len(body) > maxRequestBytes {
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "请求过大或读取失败"}})
		return
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var batch []rpcRequest
		if err := json.Unmarshal(trimmed, &batch); err != nil {
			writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "JSON 解析失败"}})
			return
		}
		var out []rpcResponse
		for _, req := range batch {
			if resp, ok := s.handle(r.Context(), req); ok {
				out = append(out, resp)
			}
		}
		if len(out) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeRPC(w, out)
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(trimmed, &req); err != nil {
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "JSON 解析失败"}})
		return
	}
	resp, ok := s.handle(r.Context(), req)
	if !ok {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeRPC(w, resp)
}

func (s *Server) authorized(r *http.Request) bool {
	want := strings.TrimSpace(s.token())
	if want == "" {
		return false
	}
	got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if got == "" {
		got = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// handle 返回 false 表示这是通知，不回包。
func (s *Server) handle(ctx context.Context, req rpcRequest) (rpcResponse, bool) {
	isNotification := len(req.ID) == 0 || string(req.ID) == "null"
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "jiying-console", "version": s.version},
			"instructions":    Instructions,
		}
	case "notifications/initialized", "notifications/cancelled":
		return resp, false
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		list := make([]map[string]any, 0, len(s.tools))
		for _, tool := range s.tools {
			list = append(list, tool.descriptor())
		}
		resp.Result = map[string]any{"tools": list}
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = &rpcError{-32602, "tools/call 参数无效"}
			break
		}
		tool, ok := s.byName[params.Name]
		if !ok {
			resp.Error = &rpcError{-32602, "没有这个工具：" + params.Name}
			break
		}
		resp.Result = s.call(ctx, tool, params.Arguments)
	default:
		if isNotification {
			return resp, false
		}
		resp.Error = &rpcError{-32601, "不支持的方法：" + req.Method}
	}
	if isNotification {
		return resp, false
	}
	return resp, true
}

type toolResult struct {
	Content []map[string]any `json:"content"`
	IsError bool             `json:"isError"`
}

func textResult(text string, isError bool) toolResult {
	return toolResult{Content: []map[string]any{{"type": "text", "text": text}}, IsError: isError}
}

func (s *Server) call(ctx context.Context, tool Tool, args map[string]any) toolResult {
	if args == nil {
		args = map[string]any{}
	}
	if tool.Run != nil {
		return tool.Run(ctx, s, args)
	}
	if tool.Confirm != "" {
		if confirmed, _ := args["confirm"].(bool); !confirmed {
			return textResult("需要用户确认后再执行："+tool.Confirm+"。请先把这件事告诉用户，用户同意后带 confirm=true 重新调用。", true)
		}
	}
	method, path, query, body, err := tool.request(args)
	if err != nil {
		return textResult(err.Error(), true)
	}
	return s.do(ctx, method, path, query, body)
}

// do 在进程内对自身 /api 发一次请求。
func (s *Server) do(ctx context.Context, method, path string, query url.Values, body any) toolResult {
	if !strings.HasPrefix(path, "/api/") {
		return textResult("路径必须以 /api/ 开头", true)
	}
	if strings.HasPrefix(path, "/api/auth/") || strings.HasPrefix(path, "/api/mcp/") {
		return textResult("登录与 MCP 令牌接口不对 AI 开放", true)
	}
	target := path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil && method != http.MethodGet && method != http.MethodHead {
		raw, err := json.Marshal(body)
		if err != nil {
			return textResult("请求体无法编码："+err.Error(), true)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequestWithContext(ctx, method, target, reader)
	req.RemoteAddr = "127.0.0.1:1"
	req.Host = "127.0.0.1"
	req.Header.Set("X-Internal-Token", s.internalToken)
	req.Header.Set("Accept", "application/json")
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	s.api.ServeHTTP(rec, req)
	payload := rec.Body.Bytes()
	text := string(payload)
	if ct := rec.Header().Get("Content-Type"); ct != "" && !strings.Contains(ct, "json") && !strings.HasPrefix(ct, "text/") {
		text = fmt.Sprintf("（返回 %s，%d 字节，二进制内容不展示）", ct, len(payload))
	} else if len(payload) > maxResultBytes {
		text = string(payload[:maxResultBytes]) + fmt.Sprintf("\n…（已截断，共 %d 字节；请用更精确的查询或分页）", len(payload))
	}
	if strings.TrimSpace(text) == "" {
		text = "（无返回内容）"
	}
	status := rec.Code
	return textResult(fmt.Sprintf("HTTP %d\n%s", status, text), status >= 400)
}

func writeRPC(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func loopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func loopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// sortedKeys 让工具描述稳定输出。
func sortedKeys(m map[string]Param) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// doMultipart 读本机文件拼 multipart 请求：MCP 参数传不了二进制，AI 给的是本机文件路径。
func (s *Server) doMultipart(ctx context.Context, path string, fields map[string]string, fileField, filePath string) toolResult {
	file, err := os.Open(filePath)
	if err != nil {
		return textResult("读不到文件："+err.Error(), true)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return textResult("不是普通文件："+filePath, true)
	}
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	for key, value := range fields {
		if value != "" {
			_ = form.WriteField(key, value)
		}
	}
	part, err := form.CreateFormFile(fileField, filepath.Base(filePath))
	if err != nil {
		return textResult(err.Error(), true)
	}
	if _, err := io.Copy(part, file); err != nil {
		return textResult("读取文件失败："+err.Error(), true)
	}
	if err := form.Close(); err != nil {
		return textResult(err.Error(), true)
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, path, &buf)
	req.RemoteAddr = "127.0.0.1:1"
	req.Host = "127.0.0.1"
	req.Header.Set("X-Internal-Token", s.internalToken)
	req.Header.Set("Content-Type", form.FormDataContentType())
	rec := httptest.NewRecorder()
	s.api.ServeHTTP(rec, req)
	text := rec.Body.String()
	if len(text) > maxResultBytes {
		text = text[:maxResultBytes] + "\n…（已截断）"
	}
	return textResult(fmt.Sprintf("HTTP %d\n%s", rec.Code, text), rec.Code >= 400)
}
