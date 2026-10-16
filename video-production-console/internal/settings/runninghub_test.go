package settings

import (
	"reflect"
	"testing"
	"time"
)

func TestRunningHubSettingsPersistCountAndHotApply(t *testing.T) {
	service, _, _, configured := newSettingsTestService(t, Options{})
	configured.RunningHubPerKeyConcurrency = 2
	configured.ReplicateDefaultProvider = "grok"
	configured.ReplicateDefaultVariants = 3
	if _, err := service.Update(t.Context(), configured, map[string]string{SecretRunningHubAPIKeys: " key-a\r\n\nkey-b\nkey-a "}); err != nil {
		t.Fatal(err)
	}
	runtime, err := service.Runtime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.RunningHubAPIKeys != " key-a\r\n\nkey-b\nkey-a " || runtime.RunningHubPerKeyConcurrency != 2 || runtime.ReplicateDefaultProvider != "grok" || runtime.ReplicateDefaultVariants != 3 {
		t.Fatalf("runtime=%+v keys=%q", runtime.PublicSettings, runtime.RunningHubAPIKeys)
	}
	if got := ParseRunningHubAPIKeys(runtime.RunningHubAPIKeys); !reflect.DeepEqual(got, []string{"key-a", "key-b"}) {
		t.Fatalf("parsed keys=%v", got)
	}
	view, err := service.Get(t.Context())
	if err != nil || !view.Secrets[SecretRunningHubAPIKeys].Configured || view.Secrets[SecretRunningHubAPIKeys].Count != 2 {
		t.Fatalf("view=%+v err=%v", view.Secrets[SecretRunningHubAPIKeys], err)
	}

	configured.RunningHubPerKeyConcurrency = 5
	configured.ReplicateDefaultProvider = "runninghub_h3"
	configured.ReplicateDefaultVariants = 4
	view, err = service.Update(t.Context(), configured, map[string]string{SecretRunningHubAPIKeys: "replacement"})
	if err != nil {
		t.Fatal(err)
	}
	if view.RestartRequired || view.ActivePublic.RunningHubPerKeyConcurrency != 5 || view.Secrets[SecretRunningHubAPIKeys].Count != 1 {
		t.Fatalf("hot view=%+v", view)
	}
	runtime, _ = service.Runtime(t.Context())
	if runtime.RunningHubAPIKeys != "replacement" || runtime.ReplicateDefaultProvider != "runninghub_h3" || runtime.ReplicateDefaultVariants != 4 {
		t.Fatalf("hot runtime=%+v keys=%q", runtime.PublicSettings, runtime.RunningHubAPIKeys)
	}
}

func TestRunningHubPublicSettingsDefaultsAndValidation(t *testing.T) {
	if got := publicFromValues(map[string]string{}); got.RunningHubPerKeyConcurrency != 1 || got.ReplicateDefaultProvider != "runninghub_h3" || got.ReplicateDefaultVariants != 1 {
		t.Fatalf("defaults=%+v", got)
	}
	service, _, _, configured := newSettingsTestService(t, Options{})
	for _, mutate := range []func(){
		func() { configured.RunningHubPerKeyConcurrency = 6 },
		func() { configured.RunningHubPerKeyConcurrency = 1; configured.ReplicateDefaultProvider = "other" },
		func() { configured.ReplicateDefaultVariants = 5 },
	} {
		configured.RunningHubPerKeyConcurrency = 1
		configured.ReplicateDefaultProvider = "runninghub_h3"
		configured.ReplicateDefaultVariants = 1
		mutate()
		if _, err := service.PutPublic(t.Context(), configured); err == nil {
			t.Fatalf("accepted %+v", configured)
		}
	}
}

func TestActivationAppliesRunningHubSettings(t *testing.T) {
	now := time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC)
	service, _, _, configured := newSettingsTestService(t, Options{Now: func() time.Time { return now }})
	if _, err := service.PutPublic(t.Context(), configured); err != nil {
		t.Fatal(err)
	}
	code := encodeActivationFixture(t, activationFixture{
		Version: 1, IssuedAt: now.Format(time.RFC3339),
		Public:  map[string]string{"runninghub_per_key_concurrency": "4", "replicate_default_provider": "grok", "replicate_default_variants": "2"},
		Secrets: map[string]string{SecretRunningHubAPIKeys: "one\ntwo"},
	})
	result, err := service.ActivateCode(t.Context(), code)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.AppliedPublic, []string{"replicate_default_provider", "replicate_default_variants", "runninghub_per_key_concurrency"}) || !reflect.DeepEqual(result.AppliedSecrets, []string{SecretRunningHubAPIKeys}) {
		t.Fatalf("activation=%+v", result)
	}
	runtime, err := service.Runtime(t.Context())
	if err != nil || runtime.RunningHubPerKeyConcurrency != 4 || runtime.ReplicateDefaultProvider != "grok" || runtime.ReplicateDefaultVariants != 2 || runtime.RunningHubAPIKeys != "one\ntwo" {
		t.Fatalf("runtime=%+v keys=%q err=%v", runtime.PublicSettings, runtime.RunningHubAPIKeys, err)
	}
}
