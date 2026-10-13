package montage

import (
	"context"
	"path/filepath"
	"time"

	"video-production-console/internal/agentruntime/montageplan"
	"video-production-console/internal/domain"
)

func cityReceiptForAttempt(attempt domain.RegistrationAttempt) string {
	return filepath.Join(filepath.Dir(filepath.Dir(attempt.WorkspacePath)), "citylib_reservation.json")
}

func (c *Coordinator) finishCityReservation(attempt domain.RegistrationAttempt, registered bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	if err := montageplan.FinalizeCityLibraryReservation(ctx, cityReceiptForAttempt(attempt), registered); err != nil {
		code := "citylib_release_failed"
		if registered {
			code = "citylib_commit_failed"
		}
		c.reportFailure(attempt.TaskID, code, err)
	}
}
