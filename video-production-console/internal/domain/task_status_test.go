package domain

import "testing"

func TestTaskStatusCanonicalAndPredicates(t *testing.T) {
	tests := []struct {
		status    TaskStatus
		canonical TaskStatus
		terminal  bool
		waiting   bool
		active    bool
	}{
		{TaskQueued, TaskQueued, false, false, true},
		{TaskRunning, TaskRunning, false, false, true},
		{TaskAwaitingInput, TaskAwaitingInput, false, true, true},
		{TaskWaitingInput, TaskAwaitingInput, false, true, true},
		{TaskResuming, TaskResuming, false, false, true},
		{TaskCompleted, TaskCompleted, true, false, false},
		{TaskFailed, TaskFailed, true, false, false},
		{TaskCanceled, TaskCanceled, true, false, false},
		{TaskCancelled, TaskCanceled, true, false, false},
		{TaskInterrupted, TaskInterrupted, true, false, false},
	}
	for _, test := range tests {
		t.Run(string(test.status), func(t *testing.T) {
			if got := test.status.Canonical(); got != test.canonical {
				t.Fatalf("Canonical()=%q want %q", got, test.canonical)
			}
			if got := test.status.IsTerminal(); got != test.terminal {
				t.Fatalf("IsTerminal()=%v want %v", got, test.terminal)
			}
			if got := test.status.IsWaitingForInput(); got != test.waiting {
				t.Fatalf("IsWaitingForInput()=%v want %v", got, test.waiting)
			}
			if got := test.status.IsActive(); got != test.active {
				t.Fatalf("IsActive()=%v want %v", got, test.active)
			}
		})
	}
}
