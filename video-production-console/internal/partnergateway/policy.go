package partnergateway

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

type RequestKind uint8

const (
	ChatRequest RequestKind = iota + 1
	ImageRequest
)

const (
	defaultImageModel      = "gpt-image-2"
	defaultMaxRequestBytes = int64(1 << 20)
)

var defaultTextModels = map[string]struct{}{"gpt-5.6-sol": {}, "grok-4.6": {}}
var defaultEfforts = map[string]struct{}{"low": {}, "medium": {}, "high": {}, "xhigh": {}, "max": {}, "ultra": {}}
var defaultImageSizes = map[string]struct{}{"1024x1024": {}, "1536x1024": {}, "1024x1536": {}}

type Policy struct {
	textModels      map[string]struct{}
	efforts         map[string]struct{}
	imageSizes      map[string]struct{}
	imageModel      string
	maxRequestBytes int64
}

func DefaultPolicy() Policy {
	return Policy{
		textModels:      cloneSet(defaultTextModels),
		efforts:         cloneSet(defaultEfforts),
		imageSizes:      cloneSet(defaultImageSizes),
		imageModel:      defaultImageModel,
		maxRequestBytes: defaultMaxRequestBytes,
	}
}

func (p Policy) Validate(kind RequestKind, body []byte) error {
	if p.maxRequestBytes <= 0 || int64(len(body)) > p.maxRequestBytes {
		return fmt.Errorf("request body exceeds policy limit")
	}
	if !json.Valid(body) {
		return fmt.Errorf("request body is not valid JSON")
	}

	var value any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("decode request body: %w", err)
	}
	if containsClientSecretField(value) {
		return fmt.Errorf("client-controlled upstream field is not allowed")
	}
	request, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("request body must be a JSON object")
	}

	model, ok := request["model"].(string)
	if !ok || model == "" {
		return fmt.Errorf("model is required")
	}
	switch kind {
	case ChatRequest:
		if _, allowed := p.textModels[model]; !allowed {
			return fmt.Errorf("text model is not allowed")
		}
		if effort, present := request["reasoning_effort"]; present {
			effortName, valid := effort.(string)
			if !valid {
				return fmt.Errorf("reasoning effort is not allowed")
			}
			if _, allowed := p.efforts[effortName]; !allowed {
				return fmt.Errorf("reasoning effort is not allowed")
			}
		}
	case ImageRequest:
		if model != p.imageModel {
			return fmt.Errorf("image model is not allowed")
		}
		count, ok := request["n"].(json.Number)
		if !ok {
			return fmt.Errorf("image count must be one")
		}
		countValue, ok := new(big.Rat).SetString(count.String())
		if !ok || countValue.Cmp(big.NewRat(1, 1)) != 0 {
			return fmt.Errorf("image count must be one")
		}
		size, ok := request["size"].(string)
		if !ok {
			return fmt.Errorf("image size is required")
		}
		if _, allowed := p.imageSizes[size]; !allowed {
			return fmt.Errorf("image size is not allowed")
		}
	default:
		return fmt.Errorf("request kind is not supported")
	}
	return nil
}

func cloneSet(source map[string]struct{}) map[string]struct{} {
	cloned := make(map[string]struct{}, len(source))
	for value := range source {
		cloned[value] = struct{}{}
	}
	return cloned
}

func containsClientSecretField(value any) bool {
	switch value := value.(type) {
	case map[string]any:
		for key, nested := range value {
			switch strings.ToLower(key) {
			case "api_key", "base_url", "authorization":
				return true
			}
			if containsClientSecretField(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range value {
			if containsClientSecretField(nested) {
				return true
			}
		}
	}
	return false
}
