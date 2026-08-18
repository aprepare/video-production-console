package mediacatalog

import (
	"net/url"
	"strings"
)

// CompatEndpoint joins an OpenAI-compatible base URL with a leaf such as
// "chat/completions" or "embeddings". A host-only address like
// https://api.siliconflow.cn becomes .../v1/<leaf>. An address that already
// ends with /v1 or the leaf is left intact.
func CompatEndpoint(base, leaf string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	leaf = strings.Trim(strings.TrimSpace(leaf), "/")
	if base == "" || leaf == "" {
		return ""
	}
	if strings.HasSuffix(base, "/"+leaf) {
		return base
	}
	if parsed, err := url.Parse(base); err == nil && parsed.Path != "" && parsed.Path != "/" && !strings.HasSuffix(base, "/v1") {
		return base + "/" + leaf
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/" + leaf
	}
	return base + "/v1/" + leaf
}
