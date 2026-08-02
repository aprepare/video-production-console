package baokuan

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type DependencyStatus struct {
	Name      string    `json:"name"`
	Available bool      `json:"available"`
	Message   string    `json:"message"`
	CheckedAt time.Time `json:"checked_at"`
}

type Material map[string]any
type Bundle struct {
	Materials []Material `json:"materials"`
}
type BundleRequest struct {
	FeedIDs        []string `json:"feed_ids,omitempty"`
	ObservationIDs []string `json:"observation_ids,omitempty"`
	SnippetIDs     []string `json:"snippet_ids,omitempty"`
}
type LibraryEvent struct {
	ID       string         `json:"id,omitempty"`
	Type     string         `json:"type,omitempty"`
	Sequence int64          `json:"sequence,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
}

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTPClient: &http.Client{Timeout: 15 * time.Second}}
}
func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}
func (c *Client) endpoint(path string) (string, error) {
	if c == nil || strings.TrimSpace(c.BaseURL) == "" {
		return "", fmt.Errorf("baokuan base URL is not configured")
	}
	u, err := url.Parse(strings.TrimRight(c.BaseURL, "/") + "/" + strings.TrimLeft(path, "/"))
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("unsupported baokuan URL scheme")
	}
	return u.String(), nil
}
func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any) error {
	e, err := c.endpoint(path)
	if err != nil {
		return err
	}
	var r *strings.Reader
	if body == nil {
		r = strings.NewReader("")
	} else {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, e, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("baokuan HTTP %s", resp.Status)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *Client) SearchMaterials(ctx context.Context, query url.Values) ([]Material, error) {
	e, err := c.endpoint("/api/channels/library/materials/search")
	if err != nil {
		return nil, err
	}
	u, _ := url.Parse(e)
	q := u.Query()
	for k, values := range query {
		for _, v := range values {
			q.Add(k, v)
		}
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("baokuan HTTP %s", resp.Status)
	}
	var raw struct {
		Materials []Material `json:"materials"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	return raw.Materials, nil
}
func (c *Client) GetMaterialBundle(ctx context.Context, ids BundleRequest) (Bundle, error) {
	count := len(ids.FeedIDs) + len(ids.ObservationIDs) + len(ids.SnippetIDs)
	if count > 20 {
		return Bundle{}, fmt.Errorf("material bundle exceeds 20 items")
	}
	var out Bundle
	err := c.doJSON(ctx, http.MethodPost, "/api/channels/library/materials/bundle", ids, &out)
	return out, err
}

func (c *Client) Health(ctx context.Context) DependencyStatus {
	s := DependencyStatus{Name: "baokuan_http", CheckedAt: time.Now().UTC()}
	_, err := c.SearchMaterials(ctx, url.Values{"limit": {"1"}})
	if err == nil {
		s.Available, s.Message = true, "connected"
	} else {
		s.Message = err.Error()
	}
	return s
}

func (c *Client) Subscribe(ctx context.Context, after string) (<-chan LibraryEvent, error) {
	e, err := c.endpoint("/api/channels/library/events")
	if err != nil {
		return nil, err
	}
	u, _ := url.Parse(e)
	q := u.Query()
	if after != "" {
		q.Set("after", after)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("baokuan SSE HTTP %s", resp.Status)
	}
	out := make(chan LibraryEvent, 16)
	go c.readSSE(ctx, u, resp, out)
	return out, nil
}
func (c *Client) readSSE(ctx context.Context, u *url.URL, resp *http.Response, out chan<- LibraryEvent) {
	defer close(out)
	defer resp.Body.Close()
	current := resp
	lastID := u.Query().Get("after")
	for {
		s := bufio.NewScanner(current.Body)
		var data strings.Builder
		var eventID string
		for s.Scan() {
			line := s.Text()
			if line == "" {
				if data.Len() > 0 {
					var ev LibraryEvent
					if json.Unmarshal([]byte(data.String()), &ev) == nil {
						if ev.ID == "" {
							ev.ID = eventID
						}
						if ev.ID != "" {
							lastID = ev.ID
						}
						select {
						case out <- ev:
						case <-ctx.Done():
							return
						}
					}
					data.Reset()
					eventID = ""
				}
				continue
			}
			if strings.HasPrefix(line, "data:") {
				data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
			if strings.HasPrefix(line, "id:") {
				eventID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
			}
		}
		if ctx.Err() != nil {
			return
		}
		current.Body.Close()
		select {
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
		nextURL := *u
		nextQuery := nextURL.Query()
		if lastID != "" {
			nextQuery.Set("after", lastID)
		}
		nextURL.RawQuery = nextQuery.Encode()
		nextReq, err := http.NewRequestWithContext(ctx, http.MethodGet, nextURL.String(), nil)
		if err != nil {
			return
		}
		nextReq.Header.Set("Accept", "text/event-stream")
		next, err := c.httpClient().Do(nextReq)
		if err != nil {
			continue
		}
		if next.StatusCode < 200 || next.StatusCode >= 300 {
			next.Body.Close()
			continue
		}
		current = next
	}
}

// ParseLimit clamps a user-facing limit to the library API's safe range.
func ParseLimit(raw string) int {
	n, _ := strconv.Atoi(raw)
	if n < 1 {
		n = 1
	}
	if n > 20 {
		n = 20
	}
	return n
}
