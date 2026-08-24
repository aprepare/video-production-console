package openaicompat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const copyTimeout = 120 * time.Second

// CopyClient calls POST /v1/copy for hooks or scripts.
type CopyClient interface {
	Copy(processType, sourceContent string) (string, error)
}

// HTTPCopyClient posts to {base}/v1/copy with X-API-Key.
type HTTPCopyClient struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

type copyRequest struct {
	ProcessType   string `json:"processType"`
	SourceContent string `json:"sourceContent"`
}

type copyResponse struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Task   struct {
		Status      int    `json:"status"`
		Title       string `json:"title"`
		Content     string `json:"content"`
		ProcessType string `json:"processType"`
	} `json:"task"`
}

func copyEndpoint(base string) (string, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return "", fmt.Errorf("copy api base url is required")
	}
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return "", fmt.Errorf("copy api base url is invalid")
	}
	if strings.HasSuffix(base, "/v1/copy") {
		return base, nil
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/copy", nil
	}
	return base + "/v1/copy", nil
}

func (c *HTTPCopyClient) Copy(processType, sourceContent string) (string, error) {
	processType = strings.TrimSpace(processType)
	sourceContent = strings.TrimSpace(sourceContent)
	if processType != "hooks" && processType != "scripts" {
		return "", fmt.Errorf("copy processType must be hooks or scripts")
	}
	if sourceContent == "" {
		return "", fmt.Errorf("copy sourceContent is required")
	}
	endpoint, err := copyEndpoint(c.BaseURL)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(copyRequest{ProcessType: processType, SourceContent: sourceContent})
	if err != nil {
		return "", err
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: copyTimeout}
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", strings.TrimSpace(c.APIKey))
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("copy %s: %w", processType, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("copy %s: http %d: %s", processType, resp.StatusCode, truncate(string(raw), 400))
	}
	var parsed copyResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("copy %s: decode response: %w", processType, err)
	}
	content := strings.TrimSpace(parsed.Task.Content)
	if !parsed.OK || content == "" {
		detail := strings.TrimSpace(parsed.Detail)
		if detail == "" {
			detail = "ok=false or empty task.content"
		}
		return "", fmt.Errorf("copy %s: %s", processType, detail)
	}
	return content, nil
}

func fetchCopyMaterials(client CopyClient, source string) (hooks, scripts string, err error) {
	if client == nil {
		return "", "", fmt.Errorf("copy client is required")
	}
	var wg sync.WaitGroup
	var hooksErr, scriptsErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		hooks, hooksErr = client.Copy("hooks", source)
	}()
	go func() {
		defer wg.Done()
		scripts, scriptsErr = client.Copy("scripts", source)
	}()
	wg.Wait()
	if hooksErr != nil {
		return "", "", fmt.Errorf("copy hooks: %w", hooksErr)
	}
	if scriptsErr != nil {
		return "", "", fmt.Errorf("copy scripts: %w", scriptsErr)
	}
	return hooks, scripts, nil
}
