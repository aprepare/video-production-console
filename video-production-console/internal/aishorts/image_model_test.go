package aishorts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestProjectImageModelReachesShotAndCharacterRequests(t *testing.T) {
	for _, model := range []string{"chosen-image-model", ""} {
		t.Run(model, func(t *testing.T) {
			want := model
			if want == "" {
				want = "runtime-default"
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if r.URL.Path != "/images/generations" || body["model"] != want {
					t.Errorf("request path=%s model=%v, want %s", r.URL.Path, body["model"], want)
				}
				calls++
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString([]byte("offline fixture"))}}})
			}))
			defer server.Close()
			svc := NewService(t.TempDir(), nil, nil)
			var short Short
			raw, _ := json.Marshal(map[string]any{"id": "fixture", "mode": "fable", "image_model": model})
			if err := json.Unmarshal(raw, &short); err != nil {
				t.Fatal(err)
			}
			short.Characters = []Character{{Name: "角色", Description: "动物角色", Status: ShotPending}}
			short.Shots = []Shot{{Scene: "桌上一本账本", ImageStatus: ShotPending}}
			if err := svc.store.Save(&short); err != nil {
				t.Fatal(err)
			}
			rt := Runtime{Models: Models{Image: "runtime-default"}}
			client := &GenClient{BaseURL: server.URL, APIKey: "offline"}
			if err := svc.generateCharacters(context.Background(), rt, client, short.ID); err != nil {
				t.Fatal(err)
			}
			dir := svc.store.AssetDir(short.ID)
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			if path := svc.generateShotImage(context.Background(), rt, client, short.ID, dir, 0, short.Shots[0], &short, nil); path == "" {
				t.Fatal("shot generation failed")
			}
			if calls != 2 {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
}
