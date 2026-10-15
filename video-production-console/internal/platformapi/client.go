// Package platformapi talks to the console-api gateway that parses and
// searches 抖音 / 小红书 / 微信视频号. The gateway keeps cookies and signing
// state on its own server; this client only forwards a bearer token and
// normalizes the loosely typed payloads (counts arrive as strings) into one
// Post shape the console can store.
package platformapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrNotConfigured means the gateway URL or token is missing.
var ErrNotConfigured = errors.New("platform_api_not_configured")

var ErrMediaTooLarge = errors.New("media exceeds size limit")

var (
	ErrMediaHostNotAllowed = errors.New("media host not allowed")
	ErrMediaRedirect       = errors.New("media redirect not allowed")
)

// DownloadHTTPError exposes the status without retaining a signed CDN URL.
type DownloadHTTPError struct{ StatusCode int }

func (e *DownloadHTTPError) Error() string {
	return fmt.Sprintf("download media: HTTP %d", e.StatusCode)
}

// Platform identifies the source network of a post.
type Platform string

const (
	Douyin   Platform = "douyin"
	XHS      Platform = "xhs"
	Channels Platform = "channels"
)

// Post is the normalized view of one video or note.
type Post struct {
	Platform      Platform `json:"platform"`
	ExternalID    string   `json:"external_id"`
	URL           string   `json:"url"`
	Title         string   `json:"title"`
	Text          string   `json:"text"`
	Author        string   `json:"author"`
	AuthorID      string   `json:"author_id"`
	CoverURL      string   `json:"cover_url"`
	VideoURL      string   `json:"video_url"`
	DurationS     float64  `json:"duration_s"`
	Likes         int64    `json:"likes"`
	Comments      int64    `json:"comments"`
	Shares        int64    `json:"shares"`
	Collects      int64    `json:"collects"`
	Plays         int64    `json:"plays"`
	CreatedAt     int64    `json:"created_at"`
	CountsMissing bool     `json:"-"`
}

// SearchPage carries one page of search results plus the cursor to continue.
type SearchPage struct {
	Items     []Post `json:"items"`
	HasMore   bool   `json:"has_more"`
	Cursor    string `json:"cursor"`
	SearchID  string `json:"search_id"`
	SessionID string `json:"session_id,omitempty"`
	LoginWall bool   `json:"login_wall,omitempty"`
}

// Comment is one top-level comment.
type Comment struct {
	Author string `json:"author"`
	Text   string `json:"text"`
	Likes  int64  `json:"likes"`
}

// Sentence is one transcribed line with millisecond timing.
type Sentence struct {
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
	Text    string `json:"text"`
}

// Transcript is the spoken text of a video (通义听悟 on the gateway). It is
// the only source of 口播; share captions (desc/description) never are.
type Transcript struct {
	TaskID     string     `json:"task_id"`
	Status     string     `json:"status"`
	Text       string     `json:"text"`
	Sentences  []Sentence `json:"sentences"`
	DurationMS int64      `json:"duration_ms"`
}

// Done reports whether the transcript finished with text.
func (t Transcript) Done() bool { return t.Status == "COMPLETED" }

// AppStatus mirrors one entry of the gateway's /api/status apps array.
type AppStatus struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Ready   bool   `json:"ready"`
	NeedYou bool   `json:"need_you"`
	Message string `json:"msg"`
	How     string `json:"how"`
}

// Client is safe for concurrent use.
type Client struct {
	baseURL  string
	token    string
	http     *http.Client
	download *http.Client
}

// New returns a client; an empty base URL or token yields ErrNotConfigured.
func New(baseURL, token string, httpClient *http.Client) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	token = strings.TrimSpace(token)
	if baseURL == "" || token == "" {
		return nil, ErrNotConfigured
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("platform api base URL is invalid")
	}
	download := &http.Client{Timeout: 10 * time.Minute}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	} else {
		download = httpClient
	}
	return &Client{baseURL: baseURL, token: token, http: httpClient, download: download}, nil
}

// Status reports which platform adapters are ready on the gateway.
func (c *Client) Status(ctx context.Context) ([]AppStatus, error) {
	var out struct {
		OK   bool        `json:"ok"`
		Apps []AppStatus `json:"apps"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/status", nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Apps, nil
}

// Search runs a keyword search on one platform. cursor, searchID and
// sessionID come back from the previous page unchanged.
func (c *Client) Search(ctx context.Context, platform Platform, keyword, cursor, searchID, sessionID string) (SearchPage, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return SearchPage{}, fmt.Errorf("keyword is required")
	}
	switch platform {
	case Douyin:
		query := url.Values{"keyword": {keyword}, "count": {"12"}}
		if cursor != "" {
			query.Set("offset", cursor)
		}
		if searchID != "" {
			query.Set("search_id", searchID)
		}
		var raw struct {
			envelope
			Items     []map[string]any `json:"items"`
			HasMore   bool             `json:"has_more"`
			Cursor    any              `json:"cursor"`
			SearchID  string           `json:"search_id"`
			LoginWall bool             `json:"login_wall"`
		}
		if err := c.do(ctx, http.MethodGet, "/api/douyin/search", query, nil, &raw); err != nil {
			return SearchPage{}, err
		}
		if !raw.OK {
			return SearchPage{LoginWall: raw.LoginWall}, raw.failure("抖音搜索")
		}
		page := SearchPage{HasMore: raw.HasMore, Cursor: str(raw.Cursor), SearchID: raw.SearchID, LoginWall: raw.LoginWall}
		for _, item := range raw.Items {
			if strings.TrimSpace(str(item["desc"])) == "" && strings.TrimSpace(str(item["play_url"])) == "" && strings.TrimSpace(str(item["cover"])) == "" && item["digg_count"] == nil {
				continue
			}
			page.Items = append(page.Items, douyinPost(item))
		}
		return page, nil
	case XHS:
		page := 1
		if n, err := strconv.Atoi(cursor); err == nil && n > 0 {
			page = n
		}
		query := url.Values{"keyword": {keyword}, "page": {strconv.Itoa(page)}}
		if searchID != "" {
			query.Set("search_id", searchID)
		}
		if sessionID != "" {
			query.Set("session_id", sessionID)
		}
		var raw struct {
			envelope
			Items     []map[string]any `json:"items"`
			HasMore   bool             `json:"has_more"`
			SearchID  string           `json:"search_id"`
			SessionID string           `json:"session_id"`
		}
		if err := c.do(ctx, http.MethodGet, "/api/xhs/search", query, nil, &raw); err != nil {
			return SearchPage{}, err
		}
		if !raw.OK {
			return SearchPage{}, raw.failure("小红书搜索")
		}
		out := SearchPage{HasMore: raw.HasMore, SearchID: raw.SearchID, SessionID: raw.SessionID}
		if raw.HasMore {
			out.Cursor = strconv.Itoa(page + 1)
		}
		for _, item := range raw.Items {
			out.Items = append(out.Items, xhsPost(item))
		}
		return out, nil
	default:
		return SearchPage{}, fmt.Errorf("platform %q does not support search", platform)
	}
}

// Resolve turns a share link (or a bare 小红书 note ID) into a post with
// its text and, for videos, a playable URL.
func (c *Client) Resolve(ctx context.Context, link string) (Post, error) {
	link = ExtractLink(link)
	switch DetectPlatform(link) {
	case Douyin:
		if !strings.Contains(strings.ToLower(link), "v.douyin.com") {
			return Post{}, fmt.Errorf("抖音请粘贴 App 里「分享 → 复制链接」得到的 v.douyin.com 短链接")
		}
		var raw map[string]any
		if err := c.do(ctx, http.MethodPost, "/api/douyin/share", nil, map[string]any{"url": link}, &raw); err != nil {
			return Post{}, err
		}
		if ok, _ := raw["ok"].(bool); !ok {
			return Post{}, fmt.Errorf("抖音解析失败：%s", str(raw["msg"]))
		}
		post := douyinPost(raw)
		if stats, ok := raw["statistics"].(map[string]any); ok {
			post.Likes = num(stats["digg_count"])
			post.Comments = num(stats["comment_count"])
			post.CountsMissing = countsMissing(stats, "digg_count", "comment_count")
			post.Shares = num(stats["share_count"])
			post.Plays = num(stats["play_count"])
		}
		post.Author = firstNonEmpty(post.Author, str(raw["author"]))
		post.URL = firstNonEmpty(str(raw["short"]), link)
		return post, nil
	case Channels:
		var raw map[string]any
		if err := c.do(ctx, http.MethodPost, "/api/channels/parse", nil, map[string]any{"url": link}, &raw); err != nil {
			return Post{}, err
		}
		if ok, _ := raw["ok"].(bool); !ok {
			return Post{}, fmt.Errorf("视频号解析失败：%s", str(raw["msg"]))
		}
		text := str(raw["description"])
		post := Post{
			Platform: Channels, URL: link, ExternalID: lastPathSegment(link),
			Title:    firstLine(text, 40),
			Text:     text,
			Author:   str(raw["author"]),
			VideoURL: str(raw["video_url"]),
			// cover_short is a human-readable summary, not a fetchable URL.
			CoverURL: firstURL(str(raw["cover_url"]), str(raw["cover"])),
		}
		post.CreatedAt = num(raw["createtime"])
		post.CountsMissing = true
		if counts, ok := raw["counts"].(map[string]any); ok {
			post.Likes = num(counts["like"])
			post.Comments = num(counts["comment"])
			post.CountsMissing = countsMissing(counts, "like", "comment")
			post.Shares = num(counts["forward"])
			post.Collects = num(counts["fav"])
		}
		return post, nil
	case XHS:
		id := xhsNoteID(link)
		if id == "" {
			return Post{}, fmt.Errorf("没有识别出小红书笔记 ID")
		}
		var raw struct {
			envelope
			Note map[string]any `json:"note"`
		}
		if err := c.do(ctx, http.MethodGet, "/api/xhs/note", url.Values{"id": {id}}, nil, &raw); err != nil {
			return Post{}, err
		}
		if !raw.OK {
			return Post{}, raw.failure("小红书笔记")
		}
		post := xhsPost(raw.Note)
		post.URL = link
		return post, nil
	default:
		return Post{}, fmt.Errorf("暂不支持这个链接：请粘贴抖音、小红书或视频号分享链接")
	}
}

// StartTranscription submits a 通义听悟 job for a playable media URL and
// returns immediately with the task ID. Poll with Transcription.
//
// The gateway's url field only accepts v.douyin.com / weixin.qq.com/sph
// share links; a web page URL passed as video_url is downloaded as HTML and
// fails in ffmpeg. Callers therefore pass the CDN play URL from Resolve or
// Search, never the post's web URL.
func (c *Client) StartTranscription(ctx context.Context, videoURL string) (Transcript, error) {
	videoURL = strings.TrimSpace(videoURL)
	if videoURL == "" {
		return Transcript{}, fmt.Errorf("这条内容没有可转写的视频地址")
	}
	var raw map[string]any
	if err := c.do(ctx, http.MethodPost, "/api/tingwu/transcribe", nil, map[string]any{"video_url": videoURL, "wait": false}, &raw); err != nil {
		return Transcript{}, err
	}
	return transcriptFrom(raw)
}

// Transcription polls a previously submitted 通义听悟 job.
func (c *Client) Transcription(ctx context.Context, taskID string) (Transcript, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return Transcript{}, fmt.Errorf("task id is required")
	}
	var raw map[string]any
	if err := c.do(ctx, http.MethodGet, "/api/tingwu/task", url.Values{"id": {taskID}}, nil, &raw); err != nil {
		return Transcript{}, err
	}
	return transcriptFrom(raw)
}

func transcriptFrom(raw map[string]any) (Transcript, error) {
	if ok, _ := raw["ok"].(bool); !ok {
		msg := str(raw["msg"])
		if strings.Contains(msg, "tingwu.env") {
			msg = "平台服务器未配置通义听悟"
		} else if i := strings.IndexByte(msg, '\n'); i > 0 {
			msg = msg[:i]
		}
		if msg == "" {
			msg = "上游返回失败"
		}
		return Transcript{}, fmt.Errorf("口播转写失败：%s", msg)
	}
	out := Transcript{
		TaskID:     str(raw["task_id"]),
		Status:     strings.ToUpper(str(raw["status"])),
		Text:       str(raw["transcript"]),
		DurationMS: num(raw["duration_ms"]),
	}
	if list, ok := raw["sentences"].([]any); ok {
		for _, item := range list {
			sentence, ok := item.(map[string]any)
			if !ok {
				continue
			}
			text := str(sentence["text"])
			if text == "" {
				continue
			}
			out.Sentences = append(out.Sentences, Sentence{StartMS: num(sentence["start_ms"]), EndMS: num(sentence["end_ms"]), Text: text})
		}
	}
	if out.Status == "COMPLETED" && out.Text == "" {
		return out, fmt.Errorf("口播转写完成但没有识别出文字（视频可能没有人声）")
	}
	return out, nil
}

// Comments fetches top comments for a post (抖音 and 小红书 only).
func (c *Client) Comments(ctx context.Context, platform Platform, externalID string, limit int) ([]Comment, error) {
	switch platform {
	case Douyin:
		if limit <= 0 || limit > 100 {
			limit = 30
		}
		var raw struct {
			OK       bool             `json:"ok"`
			Msg      string           `json:"msg"`
			Comments []map[string]any `json:"comments"`
		}
		if err := c.do(ctx, http.MethodPost, "/api/douyin/comments", nil, map[string]any{"aweme_id": externalID, "limit": limit}, &raw); err != nil {
			return nil, err
		}
		if !raw.OK {
			return nil, fmt.Errorf("抖音评论读取失败：%s", raw.Msg)
		}
		out := make([]Comment, 0, len(raw.Comments))
		for _, item := range raw.Comments {
			out = append(out, Comment{Author: str(item["nickname"]), Text: str(item["text"]), Likes: num(item["digg_count"])})
		}
		return out, nil
	case XHS:
		if limit <= 0 {
			limit = 30
		} else if limit > 300 {
			limit = 300
		}
		var raw struct {
			envelope
			Comments []map[string]any `json:"comments"`
		}
		callCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()
		client := *c.http
		client.Timeout = 0
		if err := c.doWithClient(callCtx, &client, http.MethodGet, "/api/xhs/comments", url.Values{"id": {externalID}, "all": {"1"}}, nil, &raw); err != nil {
			return nil, err
		}
		if !raw.OK {
			return nil, raw.failure("小红书评论")
		}
		out := make([]Comment, 0, min(limit, len(raw.Comments)))
		for _, item := range raw.Comments {
			out = append(out, xhsComment(item, false))
			if len(out) >= limit {
				break
			}
			for _, sub := range mapSlice(item["subs"]) {
				out = append(out, xhsComment(sub, true))
				if len(out) >= limit {
					break
				}
			}
			if len(out) >= limit {
				break
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("platform %q does not expose comments", platform)
	}
}

func xhsComment(item map[string]any, sub bool) Comment {
	author := firstNonEmpty(str(item["nick"]), str(item["nickname"]))
	if user, ok := item["user"].(map[string]any); ok {
		author = firstNonEmpty(author, str(user["nickname"]), str(user["nick_name"]), str(user["name"]))
	}
	if sub {
		author = "↳ " + author
	}
	return Comment{Author: author, Text: firstNonEmpty(str(item["content"]), str(item["text"])), Likes: num(firstNonNil(item["like"], item["like_count"], item["likes"]))}
}

func mapSlice(value any) []map[string]any {
	switch items := value.(type) {
	case []map[string]any:
		return items
	case []any:
		out := make([]map[string]any, 0, len(items))
		for _, value := range items {
			if item, ok := value.(map[string]any); ok {
				out = append(out, item)
			}
		}
		return out
	default:
		return nil
	}
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

// OpenMedia opens an allowlisted CDN or gateway media response without
// following redirects. The caller owns resp.Body.
func (c *Client) OpenMedia(ctx context.Context, platform Platform, rawURL, rangeHeader string) (*http.Response, error) {
	target, gateway, err := c.mediaTarget(platform, rawURL, true)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	if gateway {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	setMediaUserAgent(req, platform)
	if strings.TrimSpace(rangeHeader) != "" {
		req.Header.Set("Range", rangeHeader)
	}
	client := *c.download
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("open %s media failed", platform)
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		resp.Body.Close()
		return nil, ErrMediaRedirect
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		status := resp.StatusCode
		resp.Body.Close()
		return nil, &DownloadHTTPError{StatusCode: status}
	}
	return resp, nil
}

func (c *Client) mediaTarget(platform Platform, rawURL string, enforceAllowlist bool) (string, bool, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", false, fmt.Errorf("media URL is invalid")
	}
	gateway := strings.HasPrefix(rawURL, "/api/") || strings.HasPrefix(rawURL, "/cache/")
	if gateway {
		if err := ValidateMediaURL(platform, rawURL); err != nil {
			return "", false, err
		}
		return c.baseURL + rawURL, true, nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		if enforceAllowlist {
			return "", false, ErrMediaHostNotAllowed
		}
		return "", false, fmt.Errorf("media URL is invalid")
	}
	if enforceAllowlist {
		if err := ValidateMediaURL(platform, rawURL); err != nil {
			return "", false, err
		}
	}
	return rawURL, false, nil
}

// ValidateMediaURL applies the preview allowlist without making a network
// request. Gateway paths are limited to XHS and may not escape /api or /cache.
func ValidateMediaURL(platform Platform, rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ErrMediaHostNotAllowed
	}
	gateway := strings.HasPrefix(rawURL, "/api/") || strings.HasPrefix(rawURL, "/cache/")
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.User != nil || parsed.Fragment != "" || unsafeMediaPath(parsed.EscapedPath()) {
		return ErrMediaHostNotAllowed
	}
	if gateway {
		// Only the gateway's image proxy and note-image cache; never its JSON APIs.
		if platform != XHS || parsed.IsAbs() || parsed.Host != "" || (parsed.Path != "/api/img" && !strings.HasPrefix(parsed.Path, "/cache/notes/")) {
			return ErrMediaHostNotAllowed
		}
		return nil
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || !mediaHostAllowed(platform, parsed.Hostname()) {
		return ErrMediaHostNotAllowed
	}
	return nil
}

func unsafeMediaPath(escapedPath string) bool {
	value := escapedPath
	for range 8 {
		decoded, err := url.PathUnescape(value)
		if err != nil {
			return true
		}
		normalized := strings.ReplaceAll(decoded, `\`, "/")
		for _, segment := range strings.Split(normalized, "/") {
			if segment == "." || segment == ".." {
				return true
			}
		}
		if decoded == value {
			return false
		}
		value = decoded
	}
	return true
}

func mediaHostAllowed(platform Platform, hostname string) bool {
	hostname = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(hostname)), ".")
	if hostname == "" || hostname == "localhost" || net.ParseIP(hostname) != nil {
		return false
	}
	var suffixes []string
	switch platform {
	case Douyin:
		suffixes = []string{"douyinpic.com", "douyinvod.com", "zjcdn.com", "douyinstatic.com", "bytecdn.cn", "byteimg.com", "snssdk.com", "amemv.com"}
	case XHS:
		suffixes = []string{"xhscdn.com", "xiaohongshu.com"}
	case Channels:
		suffixes = []string{"qq.com", "qpic.cn"}
	default:
		return false
	}
	for _, suffix := range suffixes {
		if hostname == suffix || strings.HasSuffix(hostname, "."+suffix) {
			return true
		}
	}
	return false
}

func setMediaUserAgent(req *http.Request, platform Platform) {
	switch platform {
	case Douyin:
		req.Header.Set("User-Agent", "com.ss.android.ugc.aweme/310000 (Linux; U; Android 13; zh_CN)")
	case Channels:
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36 MicroMessenger/7.0.20 WindowsWechat")
	default:
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36")
	}
}

// Download streams a media URL to w. Platform CDNs require app-like user
// agents; 小红书 proxied paths (/api/img?...) go back through the gateway.
func (c *Client) Download(ctx context.Context, platform Platform, rawURL string, w io.Writer, limit int64) (int64, error) {
	target, gateway, err := c.mediaTarget(platform, rawURL, false)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, err
	}
	if gateway {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	setMediaUserAgent(req, platform)
	resp, err := c.download.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, fmt.Errorf("download %s media failed", platform)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return 0, &DownloadHTTPError{StatusCode: resp.StatusCode}
	}
	if limit <= 0 {
		limit = 1 << 30
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return n, fmt.Errorf("download media: %w", err)
	}
	if n > limit {
		return n, ErrMediaTooLarge
	}
	return n, nil
}

type envelope struct {
	OK   bool   `json:"ok"`
	Msg  string `json:"msg"`
	HTTP any    `json:"http"`
}

func (e envelope) failure(what string) error {
	detail := strings.TrimSpace(e.Msg)
	switch str(e.HTTP) {
	case "406":
		detail = "登录态过期，需要在服务器上重新抓取"
	case "461":
		detail = "出口被限流，需要换出口"
	}
	if detail == "" {
		detail = "上游返回失败"
	}
	return fmt.Errorf("%s失败：%s", what, detail)
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	return c.doWithClient(ctx, c.http, method, path, query, body, out)
}

func (c *Client) doWithClient(ctx context.Context, client *http.Client, method, path string, query url.Values, body any, out any) error {
	target := c.baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = strings.NewReader(string(raw))
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("平台接口不可达：%w", redact(err, c.token))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return fmt.Errorf("读取平台接口响应失败：%w", err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return fmt.Errorf("平台接口 Token 无效")
	case http.StatusNotFound:
		return fmt.Errorf("平台接口未启用该平台")
	default:
		return fmt.Errorf("平台接口返回 HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("平台接口响应不是 JSON：%w", err)
	}
	return nil
}

func redact(err error, token string) error {
	if token == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), token, "[redacted]"))
}

func douyinPost(item map[string]any) Post {
	id := str(item["aweme_id"])
	text := str(item["desc"])
	duration := float64(num(item["duration"]))
	if duration > 1000 { // search returns milliseconds, share returns seconds
		duration /= 1000
	}
	post := Post{
		Platform: Douyin, ExternalID: id,
		Title:         firstLine(text, 40),
		Text:          text,
		Author:        firstNonEmpty(str(item["nickname"]), str(item["author"])),
		AuthorID:      str(item["sec_uid"]),
		CoverURL:      str(item["cover"]),
		VideoURL:      str(item["play_url"]),
		DurationS:     duration,
		Likes:         num(item["digg_count"]),
		Comments:      num(item["comment_count"]),
		Shares:        num(item["share_count"]),
		Collects:      num(item["collect_count"]),
		CreatedAt:     num(item["create_time"]),
		CountsMissing: countsMissing(item, "digg_count", "comment_count"),
	}
	// 图集 posts carry their background music as play_url; it is not a video.
	if strings.EqualFold(str(item["type"]), "images") || strings.Contains(post.VideoURL, "mime_type=audio") {
		post.VideoURL, post.DurationS = "", 0
	}
	if id != "" {
		post.URL = "https://www.douyin.com/video/" + id
	}
	return post
}

func xhsPost(item map[string]any) Post {
	id := str(item["id"])
	title := str(item["title"])
	text := str(item["desc"])
	videoURL := str(item["video_url"])
	duration := float64Value(item["duration"])
	if strings.EqualFold(strings.TrimSpace(str(item["type"])), "normal") {
		videoURL = ""
		duration = 0
	}
	post := Post{
		Platform: XHS, ExternalID: id,
		Title:         firstNonEmpty(title, firstLine(text, 40)),
		Text:          strings.TrimSpace(title + "\n" + text),
		Author:        str(item["nick"]),
		AuthorID:      str(item["user_id"]),
		CoverURL:      str(item["cover"]),
		VideoURL:      videoURL,
		DurationS:     duration,
		Likes:         num(item["liked"]),
		Comments:      num(item["comments"]),
		Collects:      num(item["collected"]),
		CountsMissing: countsMissing(item, "liked", "comments"),
	}
	if id != "" {
		post.URL = "https://www.xiaohongshu.com/explore/" + id
	}
	return post
}

func countsMissing(item map[string]any, likeKey, commentKey string) bool {
	like, hasLike := item[likeKey]
	comment, hasComment := item[commentKey]
	return !hasLike || like == nil || !hasComment || comment == nil
}

func float64Value(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case json.Number:
		out, _ := v.Float64()
		return out
	case string:
		out, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return out
	default:
		return float64(num(value))
	}
}

// ExtractLink pulls the first http(s) URL out of pasted share text such as
// "7.94 复制打开抖音 https://v.douyin.com/xxx/ 看看…". Text without a URL is
// returned trimmed so bare 小红书 note IDs still resolve.
func ExtractLink(text string) string {
	text = strings.TrimSpace(text)
	start := strings.Index(text, "http://")
	if i := strings.Index(text, "https://"); i >= 0 && (start < 0 || i < start) {
		start = i
	}
	if start < 0 {
		return text
	}
	rest := text[start:]
	end := strings.IndexFunc(rest, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r > 0x7f
	})
	if end > 0 {
		rest = rest[:end]
	}
	return strings.TrimRight(rest, ".,;，。")
}

// DetectPlatform classifies a share link.
func DetectPlatform(link string) Platform {
	lower := strings.ToLower(link)
	switch {
	case strings.Contains(lower, "douyin.com"), strings.Contains(lower, "iesdouyin.com"):
		return Douyin
	case strings.Contains(lower, "weixin.qq.com/sph"), strings.Contains(lower, "channels.weixin.qq.com"):
		return Channels
	case strings.Contains(lower, "xiaohongshu.com"), strings.Contains(lower, "xhslink.com"), isHexID(strings.TrimSpace(link)):
		return XHS
	}
	return ""
}

func isHexID(value string) bool {
	if len(value) < 16 || len(value) > 32 {
		return false
	}
	for _, r := range value {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

func xhsNoteID(link string) string {
	link = strings.TrimSpace(link)
	if isHexID(link) {
		return strings.ToLower(link)
	}
	parsed, err := url.Parse(link)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if isHexID(parts[i]) {
			return strings.ToLower(parts[i])
		}
	}
	return ""
}

func lastPathSegment(link string) string {
	parsed, err := url.Parse(strings.TrimSpace(link))
	if err != nil {
		return link
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) == 0 || parts[len(parts)-1] == "" {
		return link
	}
	return parts[len(parts)-1]
}

func str(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case bool:
		return strconv.FormatBool(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

// num parses gateway counts that arrive as numbers, digit strings, or
// formatted strings such as "1.2万", "3w" or "10万+".
func num(value any) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case string:
		text := strings.TrimSuffix(strings.TrimSpace(v), "+")
		if text == "" {
			return 0
		}
		multiplier := 1.0
		switch {
		case strings.HasSuffix(text, "万"):
			multiplier, text = 10000, strings.TrimSuffix(text, "万")
		case strings.HasSuffix(text, "w"), strings.HasSuffix(text, "W"):
			multiplier, text = 10000, text[:len(text)-1]
		case strings.HasSuffix(text, "亿"):
			multiplier, text = 100000000, strings.TrimSuffix(text, "亿")
		}
		f, err := strconv.ParseFloat(strings.ReplaceAll(text, ",", ""), 64)
		if err != nil {
			return 0
		}
		return int64(f * multiplier)
	}
	return 0
}

// firstURL returns the first value that parses as an absolute http(s) URL.
func firstURL(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if parsed, err := url.Parse(value); err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" {
			return value
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstLine(text string, limit int) string {
	text = strings.TrimSpace(text)
	if i := strings.IndexAny(text, "\n#"); i > 0 {
		text = strings.TrimSpace(text[:i])
	}
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return text
}
