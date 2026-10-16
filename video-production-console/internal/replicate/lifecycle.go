package replicate

import (
	"context"
	"os"
	"path/filepath"
	"video-production-console/internal/store"
)

func (s *Service) SelectTake(ctx context.Context, id string) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, e := s.repo.GetTake(ctx, id)
	if e != nil {
		return Project{}, notFound(e)
	}
	sh, e := s.repo.GetShot(ctx, t.ShotID)
	if e != nil {
		return Project{}, notFound(e)
	}
	p, e := s.project(ctx, sh.ProjectID)
	if e != nil {
		return Project{}, e
	}
	if t.Status != "succeeded" {
		return Project{}, invalid("只能选择生成成功的版本")
	}
	f, e := s.open(t.Path)
	if e != nil {
		return Project{}, e
	}
	f.Close()
	sh.SelectedTakeID = id
	if e = s.repo.SaveShot(ctx, sh); e != nil {
		return Project{}, notFound(e)
	}
	invalidate(&p)
	p.Status = "ready"
	if e = s.repo.SaveProject(ctx, p); e != nil {
		return Project{}, e
	}
	s.refresh(ctx, p.ID)
	return s.get(ctx, p.ID)
}
func (s *Service) CancelTake(ctx context.Context, id string) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, e := s.repo.GetTake(ctx, id)
	if e != nil {
		return Project{}, notFound(e)
	}
	sh, e := s.repo.GetShot(ctx, t.ShotID)
	if e != nil {
		return Project{}, notFound(e)
	}
	if _, e = s.project(ctx, sh.ProjectID); e != nil {
		return Project{}, e
	}
	if t.Status == "queued" || t.Status == "submitting" || t.Status == "running" {
		t.Status = "canceled"
		t.Error = "任务已取消"
		if e = s.repo.SaveTake(ctx, t); e != nil {
			return Project{}, e
		}
		if j := s.jobs[id]; j != nil {
			j.cancel()
		}
		s.refresh(ctx, sh.ProjectID)
	}
	return s.get(ctx, sh.ProjectID)
}
func (s *Service) Delete(ctx context.Context, id string) error     { return s.remove(ctx, "project", id) }
func (s *Service) DeleteShot(ctx context.Context, id string) error { return s.remove(ctx, "shot", id) }
func (s *Service) DeleteTake(ctx context.Context, id string) error { return s.remove(ctx, "take", id) }
func (s *Service) remove(ctx context.Context, kind, id string) error {
	s.mu.Lock()
	pid, sid := id, ""
	var shot store.ReplicateShot
	var take store.ReplicateTake
	var e error
	if kind == "take" {
		take, e = s.repo.GetTake(ctx, id)
		if e == nil {
			shot, e = s.repo.GetShot(ctx, take.ShotID)
		}
		pid, sid = shot.ProjectID, shot.ID
	} else if kind == "shot" {
		shot, e = s.repo.GetShot(ctx, id)
		pid, sid = shot.ProjectID, shot.ID
	}
	if e != nil {
		s.mu.Unlock()
		return notFound(e)
	}
	p, e := s.repo.Get(ctx, pid)
	if e == nil {
		e = s.available(pid)
	}
	if e == nil && kind != "project" && p.Status == "stitching" {
		e = invalid("正在拼接，请完成后再删除镜头或版本")
	}
	if e != nil {
		s.mu.Unlock()
		return notFound(e)
	}
	s.deleting[pid] = true
	wait := []*job{}
	for jid, j := range s.jobs {
		if j.projectID == pid && (kind == "project" || kind == "shot" && j.shotID == sid || kind == "take" && jid == id) {
			j.cancel()
			wait = append(wait, j)
		}
	}
	s.mu.Unlock()
	for _, j := range wait {
		select {
		case <-j.done:
		case <-ctx.Done():
			s.mu.Lock()
			delete(s.deleting, pid)
			s.mu.Unlock()
			return ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	defer delete(s.deleting, pid)
	if !validID(pid) || !validID(id) {
		return invalid("文件路径无效")
	}
	switch kind {
	case "project":
		if info, err := s.root.Lstat(pid); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return invalid("项目目录不能是符号链接")
			}
			if e = s.root.RemoveAll(pid); e != nil {
				return e
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		return notFound(s.repo.DeleteProject(ctx, pid))
	case "shot":
		for _, t := range shot.Takes {
			if t.Path != "" {
				if e = s.root.Remove(t.Path); e != nil && !os.IsNotExist(e) {
					return e
				}
			}
		}
		if e = s.root.RemoveAll(filepath.Join(pid, "shots", id)); e != nil && !os.IsNotExist(e) {
			return e
		}
		e = s.repo.DeleteShot(ctx, id)
	case "take":
		if take.Path != "" {
			if e = s.root.Remove(take.Path); e != nil && !os.IsNotExist(e) {
				return e
			}
		}
		e = s.repo.DeleteTake(ctx, id)
	}
	if e != nil {
		return notFound(e)
	}
	p, e = s.repo.Get(ctx, pid)
	if e != nil {
		return e
	}
	invalidate(&p)
	if e = s.repo.SaveProject(ctx, p); e != nil {
		return e
	}
	s.refresh(ctx, pid)
	return nil
}
