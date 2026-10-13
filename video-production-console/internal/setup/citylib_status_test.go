package setup

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusWarnsAboutMigratedMediaLibrary(t *testing.T) {
	server := statusServer(t)
	cfg := statusFixture(t, server.URL)
	writeTestFile(t, filepath.Join(cfg.public.MediaRoot, "00_INDEX", "素材库已迁移.txt"), []byte("\ufeff当前主素材库：C:/隔离测试/城市库\r\n归档目录"))
	result, err := NewService(cfg, Options{}).Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	item := checkItem(t, result, "media_library", "warn")
	if !strings.Contains(item.Detail, "素材库已迁移到 C:/隔离测试/城市库") || !strings.Contains(item.Detail, "请把媒体素材目录改成新库") {
		t.Fatalf("%+v", item)
	}
}
