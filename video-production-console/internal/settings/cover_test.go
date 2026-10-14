package settings

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestCoverSettingsDefaultAndHotUpdate(t *testing.T) {
	profile := t.TempDir()
	t.Setenv("USERPROFILE", profile)
	service, _, _, public := newSettingsTestService(t, Options{})
	public.CoverOutputDir = ""
	_, err := service.PutPublic(t.Context(), public)
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(profile, "Videos", "封面图"); view.Public.CoverOutputDir != want || view.Public.CoverSkillRoot != "" {
		t.Fatalf("cover defaults: %+v", view.Public)
	}
	if _, err := service.Runtime(t.Context()); err != nil {
		t.Fatal(err)
	}
	public.CoverSkillRoot = filepath.Join(profile, "skill")
	public.CoverOutputDir = filepath.Join(profile, "library")
	_, err = service.PutPublic(t.Context(), public)
	if err != nil {
		t.Fatal(err)
	}
	view, err = service.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rt, err := service.Runtime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if view.RestartRequired || rt.CoverSkillRoot != public.CoverSkillRoot || rt.CoverOutputDir != public.CoverOutputDir {
		t.Fatalf("cover settings did not hot apply: %+v", view)
	}
	for _, field := range []string{"skill", "output"} {
		bad := public
		if field == "skill" {
			bad.CoverSkillRoot = "relative"
		} else {
			bad.CoverOutputDir = "relative"
		}
		if _, err := service.PutPublic(t.Context(), bad); !errors.Is(err, ErrInvalidSettings) {
			t.Fatalf("accepted relative %s path: %v", field, err)
		}
	}
}
