package mediacatalog

import "testing"

func TestCompatEndpointAddsV1ForHostOnlyBase(t *testing.T) {
	cases := []struct {
		base, leaf, want string
	}{
		{"https://api.siliconflow.cn", "chat/completions", "https://api.siliconflow.cn/v1/chat/completions"},
		{"https://api.siliconflow.cn/", "embeddings", "https://api.siliconflow.cn/v1/embeddings"},
		{"https://api.siliconflow.cn/v1", "chat/completions", "https://api.siliconflow.cn/v1/chat/completions"},
		{"https://api.siliconflow.cn/v1/", "embeddings", "https://api.siliconflow.cn/v1/embeddings"},
		{"https://api.siliconflow.cn/v1/chat/completions", "chat/completions", "https://api.siliconflow.cn/v1/chat/completions"},
		{"http://127.0.0.1:2001/custom", "chat/completions", "http://127.0.0.1:2001/custom/chat/completions"},
		{"", "chat/completions", ""},
	}
	for _, tc := range cases {
		if got := CompatEndpoint(tc.base, tc.leaf); got != tc.want {
			t.Fatalf("CompatEndpoint(%q, %q)=%q want %q", tc.base, tc.leaf, got, tc.want)
		}
	}
}
