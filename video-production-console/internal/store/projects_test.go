package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
)

func TestProjectRepositoryDefaultTitleAndTopicCard(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "projects.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	accountID := uuid.NewString()
	if _, err = db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, accountID, "account", now, now); err != nil {
		t.Fatal(err)
	}
	projectID := uuid.NewString()
	repo := NewProjectRepository(db)
	if err := repo.CreateProject(context.Background(), domain.Project{ID: projectID, AccountID: accountID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	p, err := repo.GetProject(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Untitled-"+projectID[:8] || p.Stage != domain.StageTopic {
		t.Fatalf("project=%+v", p)
	}
	if err := repo.SetTopicCardPath(context.Background(), projectID, "cards/topic.png", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	p, _ = repo.GetProject(context.Background(), projectID)
	if p.TopicCardPath == nil || *p.TopicCardPath != "cards/topic.png" {
		t.Fatalf("topic path=%v", p.TopicCardPath)
	}
}
