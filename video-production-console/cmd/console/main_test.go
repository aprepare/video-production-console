package main

import (
	"net/http"
	"testing"
	"time"
)

func TestNewServerHasDefensiveTimeouts(t *testing.T) {
	server := newServer("127.0.0.1:2030", http.NewServeMux())
	if server.ReadHeaderTimeout != 10*time.Second || server.ReadTimeout != 2*time.Minute ||
		server.WriteTimeout != 2*time.Minute || server.IdleTimeout != time.Minute || server.MaxHeaderBytes != 1<<20 {
		t.Fatalf("server limits = header:%v read:%v write:%v idle:%v max-header:%d",
			server.ReadHeaderTimeout, server.ReadTimeout, server.WriteTimeout, server.IdleTimeout, server.MaxHeaderBytes)
	}
}
