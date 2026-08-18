package main

import (
	"errors"
	"path/filepath"
	"testing"

	"video-production-console/internal/catalogbuilder"
)

func TestParseOptionsRejectsNonLoopbackAndResolvesConfig(t *testing.T) {
	if _, _, err := parseOptions([]string{"-listen", "0.0.0.0:2031"}); !errors.Is(err, catalogbuilder.ErrListenNotLocal) {
		t.Fatalf("got %v", err)
	}
	listen, config, err := parseOptions([]string{"-listen", "127.0.0.1:2099", "-config", "builder.json"})
	if err != nil {
		t.Fatal(err)
	}
	if listen != "127.0.0.1:2099" {
		t.Fatalf("listen=%q", listen)
	}
	if !filepath.IsAbs(config) || filepath.Base(config) != "builder.json" {
		t.Fatalf("config=%q", config)
	}
}
