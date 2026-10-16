package replicate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"video-production-console/internal/grokvideo"
)

type deleteRaceGrok struct {
	submitted                               atomic.Int32
	firstCanceled, releaseFirst, failSecond chan struct{}
}

func (g *deleteRaceGrok) Submit(context.Context, grokvideo.SubmitRequest) (grokvideo.Request, error) {
	return grokvideo.Request{ID: fmt.Sprint(g.submitted.Add(1))}, nil
}
func (g *deleteRaceGrok) Poll(ctx context.Context, id string) (grokvideo.Status, error) {
	if id == "1" {
		<-ctx.Done()
		close(g.firstCanceled)
		<-g.releaseFirst
		return grokvideo.Status{}, ctx.Err()
	}
	select {
	case <-ctx.Done():
		return grokvideo.Status{}, ctx.Err()
	case <-g.failSecond:
		return grokvideo.Status{}, errors.New("upstream failed")
	}
}
func (g *deleteRaceGrok) Download(context.Context, string, io.Writer) error { return nil }
func TestDeletingOneTakeDoesNotLoseOtherFailures(t *testing.T) {
	g := &deleteRaceGrok{firstCanceled: make(chan struct{}), releaseFirst: make(chan struct{}), failSecond: make(chan struct{})}
	var release sync.Once
	defer release.Do(func() { close(g.releaseFirst) })
	s, repo := fixture(t, g)
	p, e := s.Create(t.Context(), CreateRequest{Blank: true, VideoProvider: "grok"})
	if e != nil {
		t.Fatal(e)
	}
	p = addReady(t, s, p.ID)
	out, e := s.Generate(t.Context(), p.ID, GenerateRequest{Variants: 2})
	if e != nil {
		t.Fatal(e)
	}
	first, second := "", ""
	for _, v := range out.Project.Shots[0].Takes {
		v := waitTake(t, repo, v.ID, "running")
		if v.ProviderTaskID == "1" {
			first = v.ID
		} else {
			second = v.ID
		}
	}
	done := make(chan error, 1)
	go func() { done <- s.DeleteTake(context.Background(), first) }()
	select {
	case <-g.firstCanceled:
	case <-time.After(time.Second):
		t.Fatal("delete did not cancel")
	}
	close(g.failSecond)
	waitTake(t, repo, second, "failed")
	release.Do(func() { close(g.releaseFirst) })
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
