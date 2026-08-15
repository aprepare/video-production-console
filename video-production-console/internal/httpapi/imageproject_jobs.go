package httpapi

import "sync"

type imageProjectJobs struct {
	mu     sync.Mutex
	active map[string]struct{}
}

func newImageProjectJobs() *imageProjectJobs {
	return &imageProjectJobs{active: make(map[string]struct{})}
}

func (j *imageProjectJobs) Start(projectID string, run func()) bool {
	j.mu.Lock()
	if _, exists := j.active[projectID]; exists {
		j.mu.Unlock()
		return false
	}
	j.active[projectID] = struct{}{}
	j.mu.Unlock()
	go func() {
		defer func() {
			j.mu.Lock()
			delete(j.active, projectID)
			j.mu.Unlock()
		}()
		run()
	}()
	return true
}
