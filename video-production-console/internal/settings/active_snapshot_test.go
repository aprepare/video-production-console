package settings

import "testing"

func TestRuntimeKeepsActiveSnapshotUntilRestart(t *testing.T) {
	service, _, _, configured := newSettingsTestService(t, Options{})
	if _, err := service.PutPublic(t.Context(), configured); err != nil {
		t.Fatal(err)
	}
	active, err := service.Runtime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	configured.AppServerEnabled = !configured.AppServerEnabled
	view, err := service.Update(t.Context(), configured, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if !view.RestartRequired {
		t.Fatal("restart_required=false after restart-sensitive change")
	}
	stillActive, err := service.Runtime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if stillActive.AppServerEnabled != active.AppServerEnabled {
		t.Fatal("Runtime adopted configured app_server_enabled before restart")
	}
	if view.ActivePublic.AppServerEnabled != active.AppServerEnabled || view.Public.AppServerEnabled != configured.AppServerEnabled {
		t.Fatalf("configured/active view=%+v", view)
	}
}

func TestHotSettingsDoNotRequireRestart(t *testing.T) {
	service, _, _, configured := newSettingsTestService(t, Options{})
	if _, err := service.PutPublic(t.Context(), configured); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Runtime(t.Context()); err != nil {
		t.Fatal(err)
	}
	configured.MaxCodexConcurrency++
	configured.CodexHistoryLimit++
	view, err := service.Update(t.Context(), configured, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if view.RestartRequired {
		t.Fatal("restart_required=true for hot-only settings")
	}
}
