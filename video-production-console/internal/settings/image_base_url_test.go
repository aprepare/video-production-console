package settings

import (
	"errors"
	"testing"
)

func TestSettingsRejectsImageBaseURLQuery(t *testing.T) {
	service, _, _, configured := newSettingsTestService(t, Options{})
	configured.ImageBaseURL = "https://api.example.com/v1?token=not-allowed"
	if _, err := service.PutPublic(t.Context(), configured); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("PutPublic() error=%v want ErrInvalidSettings", err)
	}
}
