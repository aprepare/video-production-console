package aigc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

const maxImageBytes int64 = 30 << 20
const preservationPrompt = "保持画面中商品的形状、颜色、包装、文字和数量完全不变；只改变背景环境、光线和氛围"

type restyleParams struct {
	JobOptions
	Prompt   string `json:"prompt"`
	Preserve string `json:"preserve"`
	Model    string `json:"model"`
}

// Restyle starts at most two concurrent image edits and returns the queued job immediately.
func (s *Service) Restyle(ctx context.Context, merchantID string, materialIDs []string, prompt, preserve string, options ...JobOptions) (store.AIGCJob, error) {
	if strings.TrimSpace(prompt) == "" {
		return store.AIGCJob{}, ErrInvalidInput
	}
	inputs, err := s.inputs(ctx, merchantID, materialIDs, "image", 8)
	if err != nil {
		return store.AIGCJob{}, err
	}
	opt, err := s.options(ctx, merchantID, options)
	if err != nil {
		return store.AIGCJob{}, err
	}
	rt, err := s.settings(ctx)
	if err != nil {
		return store.AIGCJob{}, err
	}
	if strings.TrimSpace(rt.ImageBaseURL) == "" || strings.TrimSpace(rt.ImageAPIKey) == "" || strings.TrimSpace(rt.ImageModel) == "" {
		return store.AIGCJob{}, ErrImageAPINotConfigured
	}
	endpoint, err := editsURL(rt.ImageBaseURL)
	if err != nil {
		return store.AIGCJob{}, err
	}
	params := restyleParams{opt, prompt, preserve, rt.ImageModel}
	job, err := s.createJob(ctx, "", merchantID, "restyle", "queued", materialIDs, params, opt.OrderID)
	if err != nil {
		return job, err
	}
	go s.runRestyle(job, inputs, params, rt, endpoint)
	return job, nil
}

func (s *Service) runRestyle(job store.AIGCJob, inputs []store.MerchantMaterial, params restyleParams, rt consoleSettings.Runtime, endpoint string) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	if err := s.jobs.UpdateJobStatus(ctx, job.ID, "running", ""); err != nil {
		return
	}
	type result struct {
		id  string
		err error
	}
	results := make([]result, len(inputs))
	sem := make(chan struct{}, 2)
	var wg sync.WaitGroup
	for i, m := range inputs {
		wg.Add(1)
		go func(i int, m store.MerchantMaterial) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			resultCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			data, err := s.editImage(resultCtx, m, params, rt, endpoint)
			if err != nil {
				results[i].err = err
				return
			}
			id := uuid.NewString()
			filename := sourceStem(m) + "_restyle_01.png"
			path, err := s.materialPath(m.MerchantID, id, filename)
			if err != nil {
				results[i].err = err
				return
			}
			if err = os.WriteFile(path, data, 0600); err != nil {
				_ = os.Remove(path)
				results[i].err = err
				return
			}
			saved, err := s.recordMaterial(resultCtx, m, id, filename, path, "image", "restyle", params, rt.ImageModel)
			if err != nil {
				_ = os.Remove(path)
				results[i].err = err
				return
			}
			results[i].id = saved.ID
			if params.OrderID != "" {
				_, err = s.merchants.CreateCost(resultCtx, store.OrderCost{OrderID: params.OrderID, Kind: "api", Minutes: 0, AmountCents: params.UnitCostCents, Note: "AIGC 重绘 1 张"})
				if err != nil {
					results[i].err = fmt.Errorf("图片已生成，成本记录失败：%w", err)
				}
			}
		}(i, m)
	}
	wg.Wait()
	ids := []string{}
	failures := []string{}
	for i, result := range results {
		if result.id != "" {
			ids = append(ids, result.id)
		}
		if result.err != nil {
			failures = append(failures, fmt.Sprintf("素材 %s 处理失败：%s", inputs[i].ID, redact(result.err.Error(), rt.ImageAPIKey)))
		}
	}
	finalCtx, finalCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer finalCancel()
	if err := s.finish(finalCtx, job.ID, ids, failures); err != nil {
		_ = s.jobs.UpdateJobStatus(finalCtx, job.ID, "failed", "保存重绘作业结果失败")
	}
}

func editsURL(base string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(base), "/"))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("%w：生图服务地址不正确", ErrInvalidInput)
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/images/generations")
	if !strings.HasSuffix(parsed.Path, "/images/edits") {
		parsed.Path = strings.TrimRight(parsed.Path, "/") + "/images/edits"
	}
	return parsed.String(), nil
}

func (s *Service) editImage(ctx context.Context, m store.MerchantMaterial, params restyleParams, rt consoleSettings.Runtime, endpoint string) ([]byte, error) {
	// Revalidate before opening: the queued input may have been replaced since submission.
	path, err := s.safeMaterialPath(m.MerchantID, m.Path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxImageBytes {
		return nil, errors.New("输入图片超过 30MB 或不是普通文件")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("image", m.Filename)
	if err != nil {
		return nil, err
	}
	if _, err = io.Copy(part, io.LimitReader(f, maxImageBytes+1)); err != nil {
		return nil, err
	}
	prompt := preservationPrompt + "。\n" + params.Preserve + "\n" + params.Prompt
	for key, value := range map[string]string{"model": rt.ImageModel, "prompt": prompt, "n": "1", "size": "1024x1536", "response_format": "b64_json"} {
		if err = writer.WriteField(key, value); err != nil {
			return nil, err
		}
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+rt.ImageAPIKey)
	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("图片编辑接口返回 %d：%s", resp.StatusCode, raw)
	}
	raw, err := readLimited(resp.Body, 42<<20)
	if err != nil {
		return nil, err
	}
	var response struct {
		Data []struct {
			B64 string `json:"b64_json"`
			URL string `json:"url"`
		} `json:"data"`
	}
	if err = json.Unmarshal(raw, &response); err != nil {
		return nil, errors.New("图片编辑接口返回了无效 JSON")
	}
	if len(response.Data) == 0 {
		return nil, errors.New("图片编辑接口未返回图片")
	}
	first := response.Data[0]
	if first.B64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(first.B64)
		if err != nil {
			return nil, errors.New("图片编辑接口返回了无效图片编码")
		}
		if len(decoded) == 0 || int64(len(decoded)) > maxImageBytes {
			return nil, errors.New("生成图片为空或超过 30MB")
		}
		return decoded, nil
	}
	parsed, err := url.Parse(first.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return nil, errors.New("生成图片下载地址不正确")
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err = s.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("下载图片失败：HTTP %d", resp.StatusCode)
	}
	return readLimited(resp.Body, maxImageBytes)
}

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || int64(len(data)) > limit {
		return nil, errors.New("返回内容为空或超过大小限制")
	}
	return data, nil
}
