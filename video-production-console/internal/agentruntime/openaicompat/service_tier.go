package openaicompat

import (
	"errors"
	"strings"
)

var ErrInvalidServiceTier = errors.New("service_tier must be empty, default, priority, or fast")

// Empty preserves provider defaults; default explicitly turns Fast off.
func NormalizeServiceTier(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return "", nil
	case "default":
		return "default", nil
	case "priority", "fast":
		return "priority", nil
	default:
		return "", ErrInvalidServiceTier
	}
}

type serviceTierClient struct {
	client ChatClient
	tier   string
}

// Scope this wrapper to the writer and its repair calls, not other workflow nodes.
func withServiceTier(client ChatClient, tier string) ChatClient {
	return serviceTierClient{client: client, tier: tier}
}

func (c serviceTierClient) Chat(req ChatRequest) (ChatResponse, error) {
	tier, err := NormalizeServiceTier(c.tier)
	if err != nil {
		return ChatResponse{}, err
	}
	req.ServiceTier = tier
	return c.client.Chat(req)
}

func reviewerServiceTier(opts Options, spec *flowSpec) string {
	if spec != nil {
		if node := spec.reviewer(); node != nil {
			return node.Config.ServiceTier
		}
		return ""
	}
	return opts.ServiceTier
}
