package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
)

func TestTaskRepositoryCompletedByProjectUsesStableIDTieBreaker(t *testing.T) {
	db, err := Open(t.TempDir() + "/completed-order.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	accountID, projectID := uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, accountID, "A", "#fff", "active", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,?,?,?,?,?)`, projectID, accountID, "P", domain.StageScript, now, now); err != nil {
		t.Fatal(err)
	}
	lowID := "00000000-0000-4000-8000-000000000001"
	highID := "00000000-0000-4000-8000-000000000002"
	for _, id := range []string{lowID, highID} {
		if _, err := db.Exec(`INSERT INTO codex_tasks(id,project_id,account_id,type,skill_name,action,status,completion_phase,transport,prompt_snapshot,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, projectID, accountID, "remix", "finance-viral-remix", domain.ActionRemixEnhanced, domain.TaskCompleted, domain.CompletionRegistered, "legacy_exec", "prompt", now); err != nil {
			t.Fatal(err)
		}
	}

	completed, err := NewTaskRepository(db).CompletedByProject(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(completed) != 2 || completed[0].ID != highID || completed[1].ID != lowID {
		t.Fatalf("completed order=%v", []string{completed[0].ID, completed[1].ID})
	}
}

func TestTaskRepositoryQueuedAtPersistenceAndStableBoundaries(t *testing.T) {
	db, err := Open(t.TempDir() + "/queued-at.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	created := time.Date(2026, 8, 7, 9, 0, 0, 0, time.UTC)
	queued := created.Add(time.Second)
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, created, created); err != nil {
		t.Fatal(err)
	}
	repo := NewTaskRepository(db)
	task := domain.CodexTask{ID: "queued", AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixEnhanced, Status: domain.TaskQueued, PromptSnapshot: "p", CreatedAt: created, QueuedAt: &queued}
	if err := repo.CreateV2(ctx, task); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, task.ID)
	if err != nil || got.QueuedAt == nil || !got.QueuedAt.Equal(queued) {
		t.Fatalf("created task=%+v err=%v", got, err)
	}
	later := queued.Add(time.Hour)
	if err := repo.MarkQueued(ctx, task.ID, later); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.Get(ctx, task.ID)
	if got.QueuedAt == nil || !got.QueuedAt.Equal(queued) {
		t.Fatalf("MarkQueued overwrote first boundary: %+v", got.QueuedAt)
	}
	firstStart := created.Add(2 * time.Second)
	if _, err := db.Exec(`UPDATE codex_tasks SET started_at=? WHERE id=?`, firstStart, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.Start(ctx, task.ID, `{}`); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(ctx, task.ID, domain.TaskCompleted, "done", "", ""); err != nil {
		t.Fatal(err)
	}
	firstTerminal, err := repo.Get(ctx, task.ID)
	if err != nil || firstTerminal.FinishedAt == nil {
		t.Fatalf("first terminal=%+v err=%v", firstTerminal, err)
	}
	if err := repo.UpdateStatus(ctx, task.ID, domain.TaskFailed, "late", "x", "late"); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.Get(ctx, task.ID)
	if got.StartedAt == nil || !got.StartedAt.Equal(firstStart) || got.FinishedAt == nil || !got.FinishedAt.Equal(*firstTerminal.FinishedAt) {
		t.Fatalf("boundaries changed: start=%v finish=%v", got.StartedAt, got.FinishedAt)
	}
}

func TestTaskRepositoryEnsurePreparedTaskPublishesTaskAndManifestAtomically(t *testing.T) {
	db, err := Open(t.TempDir() + "/prepared.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	_, _ = db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, now, now)
	_, _ = db.Exec(`INSERT INTO skill_snapshots(id,name,path,sha256,files_json,modified_at,created_at) VALUES('s','finance-viral-remix','skill','abc','[]',?,?)`, now, now)
	repo := NewTaskRepository(db)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseCommit := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseCommit()
	repo.beforePreparedCommit = func() error { close(entered); <-release; return nil }
	task := domain.CodexTask{ID: "prepared", AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixEnhanced, Status: domain.TaskQueued, PromptSnapshot: "manifest prompt", ModelName: "m", ReasoningEffort: "high", CreatedAt: now}
	done := make(chan error, 1)
	go func() { _, e := repo.EnsurePreparedTask(context.Background(), task, "s", "manifest.json"); done <- e }()
	<-entered
	type listResult struct {
		tasks []domain.CodexTask
		err   error
	}
	listed := make(chan listResult, 1)
	listCtx, cancelList := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelList()
	go func() {
		tasks, e := NewTaskRepository(db).List(listCtx, "", domain.TaskQueued)
		listed <- listResult{tasks, e}
	}()
	var result listResult
	haveResult := false
	select {
	case result = <-listed:
		haveResult = true
		if result.err != nil {
			t.Fatal(result.err)
		}
		for _, got := range result.tasks {
			if got.ID == task.ID {
				t.Fatal("task visible before prepared transaction committed")
			}
		}
	case <-time.After(100 * time.Millisecond):
	}
	releaseCommit()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !haveResult {
		result = <-listed
	}
	if result.err != nil {
		t.Fatal(result.err)
	}
	snapshot, path, err := repo.PreparedManifest(context.Background(), task.ID)
	if err != nil || snapshot != "s" || path != "manifest.json" {
		t.Fatalf("snapshot=%q path=%q err=%v", snapshot, path, err)
	}
}

func TestTaskRepositoryEnsurePreparedTaskFailureRollsBackTaskRow(t *testing.T) {
	db, err := Open(t.TempDir() + "/prepared-fail.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	_, _ = db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, now, now)
	_, _ = db.Exec(`INSERT INTO skill_snapshots(id,name,path,sha256,files_json,modified_at,created_at) VALUES('s','finance-viral-remix','skill','abc','[]',?,?)`, now, now)
	repo := NewTaskRepository(db)
	repo.beforePreparedCommit = func() error { return errors.New("manifest metadata failed") }
	task := domain.CodexTask{ID: "rollback", AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixEnhanced, Status: domain.TaskQueued, PromptSnapshot: "p", ModelName: "m", ReasoningEffort: "high", CreatedAt: now}
	if _, err := repo.EnsurePreparedTask(context.Background(), task, "s", "manifest.json"); err == nil {
		t.Fatal("expected failure")
	}
	if _, err := repo.Get(context.Background(), task.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("task err=%v", err)
	}
}

func TestTaskRepositoryEnsurePreparedTaskRejectsIdentityAndRunningManifestChanges(t *testing.T) {
	db, err := Open(t.TempDir() + "/prepared-conflict.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	_, _ = db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, now, now)
	_, _ = db.Exec(`INSERT INTO skill_snapshots(id,name,path,sha256,files_json,modified_at,created_at) VALUES('s','finance-viral-remix','skill','abc','[]',?,?)`, now, now)
	repo := NewTaskRepository(db)
	task := domain.CodexTask{ID: "conflict", AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixEnhanced, Status: domain.TaskQueued, PromptSnapshot: "p", ModelName: "m", ReasoningEffort: "high", CreatedAt: now}
	if _, err := repo.EnsurePreparedTask(context.Background(), task, "s", "manifest.json"); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.EnsurePreparedTask(context.Background(), task, "s", "manifest.json"); err != nil || got.ID != task.ID {
		t.Fatalf("idempotent ensure=%+v err=%v", got, err)
	}
	changed := task
	changed.Action = domain.ActionRemixStandard
	if _, err := repo.EnsurePreparedTask(context.Background(), changed, "s", "manifest.json"); err == nil {
		t.Fatal("expected identity conflict")
	}
	if err := repo.UpdateStatus(context.Background(), task.ID, domain.TaskRunning, "", "", ""); err != nil {
		t.Fatal(err)
	}
	changed = task
	changed.PromptSnapshot = "replacement"
	if _, err := repo.EnsurePreparedTask(context.Background(), changed, "s", "other.json"); err == nil {
		t.Fatal("expected running manifest conflict")
	}
	snapshot, path, err := repo.PreparedManifest(context.Background(), task.ID)
	if err != nil || snapshot != "s" || path != "manifest.json" {
		t.Fatalf("snapshot=%q path=%q err=%v", snapshot, path, err)
	}
}

func TestTaskRepositoryPersistsTaskEventsAndStatus(t *testing.T) {
	db, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	if _, err = db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	r := NewTaskRepository(db)
	task := domain.CodexTask{ID: "t1", AccountID: "a", Type: "topic_select", SkillName: "finance-topic-selector", Status: domain.TaskQueued, PromptSnapshot: "p", CreatedAt: now}
	if err := r.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if err := r.AppendEvent(context.Background(), task.ID, domain.TaskEvent{ID: "e1", Kind: "agent_message", Level: "info", DisplayText: "hello", RawJSON: `{"type":"x"}`, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateStatus(context.Background(), task.ID, domain.TaskCompleted, "done", "", ""); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.TaskCompleted || got.ResultSummary == nil || *got.ResultSummary != "done" {
		t.Fatalf("got=%+v", got)
	}
	events, err := r.Events(context.Background(), task.ID)
	if err != nil || len(events) != 1 || events[0].Sequence != 1 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}

func TestTaskRepositoryCreateV2PersistsActionForRunnerValidation(t *testing.T) {
	db, err := Open(t.TempDir() + "/task-v2.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	repo := NewTaskRepository(db)
	task := domain.CodexTask{ID: "v2", AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixEnhanced, Status: domain.TaskQueued, PromptSnapshot: "p", CreatedAt: now}
	if err := repo.CreateV2(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if action, err := repo.ExpectedAction(context.Background(), task.ID); err != nil || action != domain.ActionRemixEnhanced {
		t.Fatalf("ExpectedAction = %q, %v", action, err)
	}
	got, err := repo.Get(context.Background(), task.ID)
	if err != nil || got.Action != domain.ActionRemixEnhanced {
		t.Fatalf("Get = %#v, %v", got, err)
	}
	if err := repo.CreateV2(context.Background(), domain.CodexTask{ID: "missing-action", AccountID: "a", SkillName: "finance-viral-remix", Status: domain.TaskQueued, PromptSnapshot: "p", CreatedAt: now}); err == nil {
		t.Fatal("expected missing action to be rejected")
	}
}

func TestTaskRepositoryRoundTripsTaskModelSelectionAndDefaultsBlanks(t *testing.T) {
	db, err := Open(t.TempDir() + "/task-model.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	repo := NewTaskRepository(db)
	for _, task := range []domain.CodexTask{
		{ID: "explicit", AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixStandard, Status: domain.TaskQueued, PromptSnapshot: "p", ModelName: "openai/custom", ReasoningEffort: "high", CreatedAt: now},
		{ID: "defaulted", AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixStandard, Status: domain.TaskQueued, PromptSnapshot: "p", CreatedAt: now.Add(time.Second)},
	} {
		if err := repo.CreateV2(context.Background(), task); err != nil {
			t.Fatal(err)
		}
	}
	wants := map[string][2]string{"explicit": {"openai/custom", "high"}, "defaulted": {"gpt-5.6-sol", "medium"}}
	for id, want := range wants {
		got, err := repo.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if got.ModelName != want[0] || got.ReasoningEffort != want[1] {
			t.Fatalf("Get(%s) model=%q effort=%q", id, got.ModelName, got.ReasoningEffort)
		}
	}
	listed, err := repo.List(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].ModelName != "gpt-5.6-sol" || listed[1].ModelName != "openai/custom" {
		t.Fatalf("List=%#v", listed)
	}
}

func TestTaskRepositoryRoundTripsAppServerTransportMetadata(t *testing.T) {
	db, err := Open(t.TempDir() + "/task-transport.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	chatSessionID, threadID, turnID := "chat-1", "thread-1", "turn-1"
	if err := NewConversationRepository(db).CreateSession(context.Background(), domain.ChatSession{ID: chatSessionID, Title: "task chat", Kind: domain.ChatProject, Status: domain.ChatRunning}); err != nil {
		t.Fatal(err)
	}
	repo := NewTaskRepository(db)
	task := domain.CodexTask{
		ID: "app-server", AccountID: "a", Type: "remix", SkillName: "finance-viral-remix",
		Action: domain.ActionRemixStandard, Status: domain.TaskRunning, PromptSnapshot: "p",
		ChatSessionID: &chatSessionID, CodexThreadID: &threadID, CodexTurnID: &turnID,
		CompletionPhase: "agent_running", Transport: "app_server", CreatedAt: now,
	}
	if err := repo.CreateV2(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ChatSessionID == nil || *got.ChatSessionID != chatSessionID || got.CodexThreadID == nil || *got.CodexThreadID != threadID || got.CodexTurnID == nil || *got.CodexTurnID != turnID || got.CompletionPhase != "agent_running" || got.Transport != "app_server" {
		t.Fatalf("task=%+v", got)
	}
	listed, err := repo.List(context.Background(), "", "")
	if err != nil || len(listed) != 1 || listed[0].Transport != "app_server" || listed[0].CodexTurnID == nil || *listed[0].CodexTurnID != turnID {
		t.Fatalf("List=%+v err=%v", listed, err)
	}
	var legacyTransport, legacyPhase string
	if _, err := db.Exec(`INSERT INTO codex_tasks(id,account_id,type,skill_name,status,prompt_snapshot,created_at) VALUES('legacy','a','remix','finance-viral-remix','queued','p',?)`, now); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT transport,completion_phase FROM codex_tasks WHERE id='legacy'`).Scan(&legacyTransport, &legacyPhase); err != nil {
		t.Fatal(err)
	}
	if legacyTransport != "legacy_exec" || legacyPhase != "agent_running" {
		t.Fatalf("legacy defaults=%q/%q", legacyTransport, legacyPhase)
	}
}

func TestTaskRepositoryCreateAndCreateV2RejectUnknownTransportMetadata(t *testing.T) {
	db, err := Open(t.TempDir() + "/task-transport-validation.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	repo := NewTaskRepository(db)
	tests := []struct {
		name, transport, phase string
		v2                     bool
	}{
		{name: "create transport", transport: "unknown", phase: "agent_running"},
		{name: "create phase", transport: "legacy_exec", phase: "unknown"},
		{name: "create v2 transport", transport: "unknown", phase: "agent_running", v2: true},
		{name: "create v2 phase", transport: "app_server", phase: "unknown", v2: true},
	}
	for index, test := range tests {
		task := domain.CodexTask{ID: test.name, AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixStandard, Status: domain.TaskQueued, PromptSnapshot: "p", Transport: test.transport, CompletionPhase: test.phase, CreatedAt: now.Add(time.Duration(index) * time.Second)}
		var createErr error
		if test.v2 {
			createErr = repo.CreateV2(context.Background(), task)
		} else {
			createErr = repo.Create(context.Background(), task)
		}
		if createErr == nil {
			t.Fatalf("%s accepted transport=%q phase=%q", test.name, test.transport, test.phase)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM codex_tasks`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("invalid tasks persisted=%d", count)
	}
}

func TestTaskRepositoryCreateTransportDefaultsAndExplicitAppServer(t *testing.T) {
	db, err := Open(t.TempDir() + "/task-transport-valid.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	repo := NewTaskRepository(db)
	if err := repo.Create(context.Background(), domain.CodexTask{ID: "default", AccountID: "a", Type: "legacy", SkillName: "skill", Status: domain.TaskQueued, PromptSnapshot: "p", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateV2(context.Background(), domain.CodexTask{ID: "app", AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixStandard, Status: domain.TaskCompleted, PromptSnapshot: "p", Transport: " app_server ", CompletionPhase: " registered ", CreatedAt: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string][2]string{"default": {"legacy_exec", "agent_running"}, "app": {"app_server", "registered"}} {
		got, err := repo.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Transport != want[0] || got.CompletionPhase != want[1] {
			t.Fatalf("%s transport/phase=%q/%q", id, got.Transport, got.CompletionPhase)
		}
	}
}

func TestTaskRepositoryInterruptInFlightNeverRequeuesAndWritesEvent(t *testing.T) {
	db, err := Open(t.TempDir() + "/task-recovery.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	repo := NewTaskRepository(db)
	for _, task := range []domain.CodexTask{
		{ID: "running", AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixStandard, Status: domain.TaskRunning, PromptSnapshot: "p", CreatedAt: now},
		{ID: "resuming", AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixStandard, Status: domain.TaskResuming, PromptSnapshot: "p", CreatedAt: now},
		{ID: "queued", AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixStandard, Status: domain.TaskQueued, PromptSnapshot: "p", CreatedAt: now},
	} {
		if err := repo.CreateV2(context.Background(), task); err != nil {
			t.Fatal(err)
		}
	}
	count, err := repo.InterruptInFlight(context.Background())
	if err != nil || count != 2 {
		t.Fatalf("InterruptInFlight = %d, %v", count, err)
	}
	for _, id := range []string{"running", "resuming"} {
		got, err := repo.Get(context.Background(), id)
		if err != nil || got.Status != domain.TaskInterrupted || got.ErrorCode == nil || *got.ErrorCode != "interrupted_on_restart" {
			t.Fatalf("recovered task %s = %#v, %v", id, got, err)
		}
		events, err := repo.Events(context.Background(), id)
		if err != nil || len(events) != 1 || events[0].Kind != "interrupted_on_restart" {
			t.Fatalf("recovery events for %s = %#v, %v", id, events, err)
		}
	}
	queued, err := repo.Get(context.Background(), "queued")
	if err != nil || queued.Status != domain.TaskQueued {
		t.Fatalf("queued task changed: %#v, %v", queued, err)
	}
}

func TestInterruptInFlightLeavesUnsentFormalOutboxForBrokerRecovery(t *testing.T) {
	db, err := Open(t.TempDir() + "/formal-recovery-order.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	sessionID, threadID, taskID := uuid.NewString(), "thread-formal-recovery", uuid.NewString()
	conversations := NewConversationRepository(db)
	if err := conversations.CreateSession(context.Background(), domain.ChatSession{ID: sessionID, Title: "formal", Kind: domain.ChatGeneral, Status: domain.ChatIdle, CodexThreadID: &threadID}); err != nil {
		t.Fatal(err)
	}
	repo := NewTaskRepository(db)
	task := domain.CodexTask{ID: taskID, AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixStandard, Status: domain.TaskRunning, ChatSessionID: &sessionID, CodexThreadID: &threadID, Transport: "app_server", CompletionPhase: string(domain.CompletionAgentRunning), PromptSnapshot: "prompt", CreatedAt: now}
	if err := repo.CreateV2(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if _, err := conversations.Enqueue(context.Background(), sessionID, formalTaskClientKeyPrefix+taskID+":initial", "prompt", domain.DeliveryQueue); err != nil {
		t.Fatal(err)
	}
	count, err := repo.InterruptInFlight(context.Background())
	if err != nil || count != 0 {
		t.Fatalf("InterruptInFlight=%d err=%v", count, err)
	}
	got, err := repo.Get(context.Background(), taskID)
	if err != nil || got.Status != domain.TaskRunning {
		t.Fatalf("task=%+v err=%v", got, err)
	}
}

func TestInterruptInFlightLeavesPersistedCompletionForBrokerReplay(t *testing.T) {
	for _, inboxState := range []domain.ChatCompletionInboxStatus{domain.ChatCompletionPending, domain.ChatCompletionProcessing} {
		t.Run(string(inboxState), func(t *testing.T) {
			db, err := Open(t.TempDir() + "/formal-completion-order.db")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			now := time.Now().UTC()
			if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, now, now); err != nil {
				t.Fatal(err)
			}
			sessionID, threadID, turnID, taskID := uuid.NewString(), "thread-completion-recovery", "turn-completion-recovery", uuid.NewString()
			conversations := NewConversationRepository(db)
			if err := conversations.CreateSession(context.Background(), domain.ChatSession{ID: sessionID, Title: "formal", Kind: domain.ChatGeneral, Status: domain.ChatRunning, CodexThreadID: &threadID}); err != nil {
				t.Fatal(err)
			}
			repo := NewTaskRepository(db)
			task := domain.CodexTask{ID: taskID, AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixStandard, Status: domain.TaskResuming, ChatSessionID: &sessionID, CodexThreadID: &threadID, CodexTurnID: &turnID, Transport: "app_server", CompletionPhase: string(domain.CompletionAgentRunning), PromptSnapshot: "prompt", CreatedAt: now}
			if err := repo.CreateV2(context.Background(), task); err != nil {
				t.Fatal(err)
			}
			if err := conversations.EnqueueCompletion(context.Background(), sessionID, turnID); err != nil {
				t.Fatal(err)
			}
			if inboxState == domain.ChatCompletionProcessing {
				if _, claimed, err := conversations.ClaimCompletion(context.Background(), true); err != nil || !claimed {
					t.Fatalf("claim completion=%v err=%v", claimed, err)
				}
			}
			count, err := repo.InterruptInFlight(context.Background())
			if err != nil || count != 0 {
				t.Fatalf("InterruptInFlight=%d err=%v", count, err)
			}
			got, err := repo.Get(context.Background(), taskID)
			if err != nil || got.Status != domain.TaskRunning {
				t.Fatalf("task=%+v err=%v", got, err)
			}
		})
	}
}

func newTaskResultRepository(t *testing.T) (*sql.DB, *TaskRepository, domain.CodexTask) {
	t.Helper()
	db, err := Open(t.TempDir() + "/task-result.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now().UTC()
	accountID := uuid.NewString()
	projectID := uuid.NewString()
	taskID := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, accountID, "A", "#fff", "active", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,?,?,?,?,?)`, projectID, accountID, "P", domain.StageScript, now, now); err != nil {
		t.Fatal(err)
	}
	repo := NewTaskRepository(db)
	task := domain.CodexTask{ID: taskID, ProjectID: &projectID, AccountID: accountID, Type: "remix", SkillName: "finance-viral-remix", Status: domain.TaskQueued, PromptSnapshot: "prompt", CreatedAt: now}
	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE codex_tasks SET action=? WHERE id=?`, domain.ActionRemixStandard, task.ID); err != nil {
		t.Fatal(err)
	}
	return db, repo, task
}

func TestTaskRepositoryStartsWithPersistedCommandSnapshot(t *testing.T) {
	db, repo, task := newTaskResultRepository(t)
	snapshot := `{"binary":"codex","args":["exec"],"environment_keys":[]}`
	if err := repo.Start(context.Background(), task.ID, snapshot); err != nil {
		t.Fatal(err)
	}
	var status domain.TaskStatus
	var got string
	if err := db.QueryRow(`SELECT status,config_snapshot_json FROM codex_tasks WHERE id=?`, task.ID).Scan(&status, &got); err != nil {
		t.Fatal(err)
	}
	if status != domain.TaskRunning || got != snapshot {
		t.Fatalf("status=%s snapshot=%q", status, got)
	}
}

func TestClaimAppServerResultAllowsOneConcurrentOwner(t *testing.T) {
	_, repo, task := newTaskResultRepository(t)
	threadID, turnID := "thread-claim", "turn-claim"
	if err := repo.SetTransportMetadata(context.Background(), task.ID, nil, &threadID, &turnID, string(domain.CompletionAgentRunning), "app_server"); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(context.Background(), task.ID, domain.TaskRunning, "", "", ""); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	errors := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := repo.ClaimAppServerResult(context.Background(), task.ID, turnID)
			results <- claimed
			errors <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errors)
	claimedCount := 0
	for claimed := range results {
		if claimed {
			claimedCount++
		}
	}
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if claimedCount != 1 {
		t.Fatalf("claims=%d, want exactly one", claimedCount)
	}
}

func TestBeginAppServerResumeRollsBackStateWhenAnswerWriteFails(t *testing.T) {
	db, repo, task := newTaskResultRepository(t)
	threadID, turnID, sessionID := "thread-resume", "turn-resume", uuid.NewString()
	if err := NewConversationRepository(db).CreateSession(context.Background(), domain.ChatSession{ID: sessionID, Title: "resume", Kind: domain.ChatGeneral, Status: domain.ChatIdle, CodexThreadID: &threadID}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetTransportMetadata(context.Background(), task.ID, &sessionID, &threadID, &turnID, string(domain.CompletionAgentRunning), "app_server"); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(context.Background(), task.ID, domain.TaskAwaitingInput, "question", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_resume_answer BEFORE INSERT ON task_messages BEGIN SELECT RAISE(ABORT,'reject answer'); END`); err != nil {
		t.Fatal(err)
	}
	clientKey := "__formal_task__:" + task.ID + ":resume:" + turnID
	if err := repo.BeginAppServerResume(context.Background(), task.ID, turnID, sessionID, clientKey, "answer"); err == nil {
		t.Fatal("expected answer persistence failure")
	}
	got, err := repo.Get(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.TaskAwaitingInput || got.CodexTurnID == nil || *got.CodexTurnID != turnID {
		t.Fatalf("task changed after rolled-back resume: %+v", got)
	}
	if _, err := db.Exec(`DROP TRIGGER reject_resume_answer`); err != nil {
		t.Fatal(err)
	}
	if err := repo.BeginAppServerResume(context.Background(), task.ID, turnID, sessionID, clientKey, "answer"); err != nil {
		t.Fatal(err)
	}
	got, err = repo.Get(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.TaskRunning || got.CodexTurnID != nil {
		t.Fatalf("resume did not atomically clear the old turn: %+v", got)
	}
	messages, err := repo.Messages(context.Background(), task.ID)
	if err != nil || len(messages) != 1 || messages[0].Content != "answer" {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	pending, err := NewConversationRepository(db).PendingOutbox(context.Background(), sessionID)
	if err != nil || len(pending) != 1 || pending[0].ClientKey != clientKey {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
}

func TestTaskRepositoryAwaitInputPersistsConversationAtomically(t *testing.T) {
	_, repo, task := newTaskResultRepository(t)
	if err := repo.Start(context.Background(), task.ID, `{}`); err != nil {
		t.Fatal(err)
	}
	question := `[{"text":"pick","options":["a","b"]}]`
	write := TaskResultWrite{Status: domain.TaskAwaitingInput, Summary: "choose", AssistantContent: "choose", QuestionSchema: &question, EventKind: "result_awaiting_input", RawJSON: `{"status":"awaiting_input"}`}
	if err := repo.AwaitInput(context.Background(), task.ID, write); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.TaskAwaitingInput || got.ResultSummary == nil || *got.ResultSummary != "choose" {
		t.Fatalf("task=%+v", got)
	}
	messages, err := repo.Messages(context.Background(), task.ID)
	if err != nil || len(messages) != 1 || messages[0].Role != "assistant" || messages[0].QuestionSchema == nil || *messages[0].QuestionSchema != question {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	events, err := repo.Events(context.Background(), task.ID)
	if err != nil || len(events) != 1 || events[0].Kind != "result_awaiting_input" {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}

func TestTaskRepositoryAwaitInputRollsBackMessageAndEventOnStatusFailure(t *testing.T) {
	db, repo, task := newTaskResultRepository(t)
	if err := repo.Start(context.Background(), task.ID, `{}`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_awaiting BEFORE UPDATE ON codex_tasks WHEN NEW.status='awaiting_input' BEGIN SELECT RAISE(ABORT,'reject awaiting'); END`); err != nil {
		t.Fatal(err)
	}
	question := `[{"text":"pick","options":[]}]`
	err := repo.AwaitInput(context.Background(), task.ID, TaskResultWrite{Status: domain.TaskAwaitingInput, Summary: "choose", AssistantContent: "choose", QuestionSchema: &question, EventKind: "result_awaiting_input", RawJSON: `{}`})
	if err == nil {
		t.Fatal("expected transaction failure")
	}
	assertTaskResultCounts(t, db, task.ID, domain.TaskRunning, 0, 0, 0, 0)
}

func TestTaskRepositoryCompleteSeparatesArtifactsAndFormalAssets(t *testing.T) {
	db, repo, task := newTaskResultRepository(t)
	if err := repo.Start(context.Background(), task.ID, `{}`); err != nil {
		t.Fatal(err)
	}
	artifact := TaskArtifact{Kind: "qc_report", Path: `C:\managed\qc.json`, Filename: "qc.json", MIMEType: "application/json", Size: 12, SHA256: "artifact-sha"}
	asset := AddAssetVersion{ProjectID: task.ProjectID, Type: domain.AssetContinuousScript, StorageKind: domain.StorageFile, Path: `C:\managed\script.md`, Filename: "script.md", MIMEType: "text/markdown", Size: 6, SHA256: "asset-sha", SourceTaskID: &task.ID}
	write := TaskResultWrite{Status: domain.TaskCompleted, Summary: "done", AssistantContent: "done", EventKind: "result_completed", RawJSON: `{"status":"completed"}`}
	if err := repo.CompleteWithResult(context.Background(), task.ID, write, []TaskArtifact{artifact}, []AddAssetVersion{asset}); err != nil {
		t.Fatal(err)
	}
	assertTaskResultCounts(t, db, task.ID, domain.TaskCompleted, 1, 1, 1, 1)
	var artifactPath, assetPath string
	if err := db.QueryRow(`SELECT path FROM task_artifacts WHERE task_id=?`, task.ID).Scan(&artifactPath); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT path FROM asset_versions WHERE source_task_id=?`, task.ID).Scan(&assetPath); err != nil {
		t.Fatal(err)
	}
	if artifactPath == assetPath {
		t.Fatalf("engineering artifact was registered as formal asset: %q", artifactPath)
	}
}

func TestTaskRepositoryCompleteRollsBackAllResultWritesWhenAssetFails(t *testing.T) {
	db, repo, task := newTaskResultRepository(t)
	if err := repo.Start(context.Background(), task.ID, `{}`); err != nil {
		t.Fatal(err)
	}
	artifact := TaskArtifact{Kind: "qc_report", Path: `C:\managed\qc.json`, Filename: "qc.json", MIMEType: "application/json", Size: 12, SHA256: "artifact-sha"}
	valid := AddAssetVersion{ProjectID: task.ProjectID, Type: domain.AssetContinuousScript, StorageKind: domain.StorageFile, Path: `C:\managed\script.md`, Filename: "script.md", MIMEType: "text/markdown", Size: 6, SHA256: "asset-sha", SourceTaskID: &task.ID}
	invalid := valid
	invalid.Type = ""
	err := repo.CompleteWithResult(context.Background(), task.ID, TaskResultWrite{Status: domain.TaskCompleted, Summary: "done", AssistantContent: "done", EventKind: "result_completed", RawJSON: `{}`}, []TaskArtifact{artifact}, []AddAssetVersion{valid, invalid})
	if err == nil {
		t.Fatal("expected formal asset failure")
	}
	assertTaskResultCounts(t, db, task.ID, domain.TaskRunning, 0, 0, 0, 0)
	var items int
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_items`).Scan(&items); err != nil || items != 0 {
		t.Fatalf("asset_items=%d err=%v", items, err)
	}
}

func TestClaimedAppServerResultCannotWriteAfterCancellation(t *testing.T) {
	db, repo, task := newTaskResultRepository(t)
	threadID, turnID := "thread-cancel-race", "turn-cancel-race"
	if err := repo.SetTransportMetadata(context.Background(), task.ID, nil, &threadID, &turnID, string(domain.CompletionAgentRunning), "app_server"); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(context.Background(), task.ID, domain.TaskRunning, "", "", ""); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimAppServerResult(context.Background(), task.ID, turnID)
	if err != nil || !claimed {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	canceled, err := repo.CancelAppServerTurn(context.Background(), task.ID, turnID)
	if err != nil || !canceled {
		t.Fatalf("cancel=%v err=%v", canceled, err)
	}
	artifact := TaskArtifact{Kind: "qc_report", Path: `C:\managed\qc.json`, Filename: "qc.json", MIMEType: "application/json", Size: 12, SHA256: "cancel-race-artifact"}
	asset := AddAssetVersion{ProjectID: task.ProjectID, Type: domain.AssetContinuousScript, StorageKind: domain.StorageFile, Path: `C:\managed\script.md`, Filename: "script.md", MIMEType: "text/markdown", Size: 6, SHA256: "cancel-race-asset", SourceTaskID: &task.ID}
	write := TaskResultWrite{Status: domain.TaskCompleted, Summary: "must not persist", AssistantContent: "must not persist", EventKind: "result_completed", RawJSON: `{}`, ExpectedTurnID: &turnID}
	if err := repo.CompleteWithResult(context.Background(), task.ID, write, []TaskArtifact{artifact}, []AddAssetVersion{asset}); err == nil {
		t.Fatal("expected canceled task to reject claimed result")
	}
	assertTaskResultCounts(t, db, task.ID, domain.TaskCanceled, 0, 0, 0, 0)
}

func TestFailAppServerTurnDoesNotOverwriteTerminalTask(t *testing.T) {
	for _, status := range []domain.TaskStatus{domain.TaskCanceled, domain.TaskCompleted} {
		t.Run(string(status), func(t *testing.T) {
			_, repo, task := newTaskResultRepository(t)
			threadID, turnID := "thread-terminal", "turn-terminal"
			if err := repo.SetTransportMetadata(context.Background(), task.ID, nil, &threadID, &turnID, string(domain.CompletionAgentRunning), "app_server"); err != nil {
				t.Fatal(err)
			}
			if err := repo.UpdateStatus(context.Background(), task.ID, status, "terminal", "", ""); err != nil {
				t.Fatal(err)
			}
			failed, err := repo.FailAppServerTurn(context.Background(), task.ID, turnID, "late_failure", "must not overwrite")
			if err != nil {
				t.Fatal(err)
			}
			if failed {
				t.Fatal("terminal task was overwritten by late failure")
			}
			got, err := repo.Get(context.Background(), task.ID)
			if err != nil || got.Status != status {
				t.Fatalf("task=%+v err=%v", got, err)
			}
		})
	}
}

func TestTopicCandidatesRollBackWithTaskResult(t *testing.T) {
	db, repo, task := newTaskResultRepository(t)
	ideas := NewIdeaRepository(db)
	sessionID := uuid.NewString()
	if err := ideas.CreateSession(context.Background(), domain.IdeaSession{ID: sessionID, Title: "brainstorm", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if err := ideas.AddMessage(context.Background(), domain.IdeaMessage{SessionID: sessionID, TaskID: &task.ID, Role: "user", Content: "brainstorm"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Start(context.Background(), task.ID, `{}`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_brainstorm_completion BEFORE UPDATE ON codex_tasks WHEN NEW.status='completed' BEGIN SELECT RAISE(ABORT,'reject completion'); END`); err != nil {
		t.Fatal(err)
	}
	write := TaskResultWrite{
		Status: domain.TaskCompleted, Summary: "ideas", AssistantContent: "ideas", EventKind: "result_completed", RawJSON: `{}`,
		IdeaSessionID: sessionID, IdeaCandidates: []domain.IdeaCandidate{{ID: uuid.NewString(), Title: "Candidate", Summary: "Summary", Score: 1, Source: "agent"}},
	}
	if err := repo.CompleteWithResult(context.Background(), task.ID, write, nil, nil); err == nil {
		t.Fatal("expected task completion failure")
	}
	assertTaskResultCounts(t, db, task.ID, domain.TaskRunning, 0, 0, 0, 0)
	candidates, err := ideas.Candidates(context.Background(), sessionID)
	if err != nil || len(candidates) != 0 {
		t.Fatalf("candidates=%+v err=%v", candidates, err)
	}
}

func TestAppendEventConcurrentSequencesAreUnique(t *testing.T) {
	db, repo, task := newTaskResultRepository(t)
	const count = 16
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			errs <- repo.AppendEvent(context.Background(), task.ID, domain.TaskEvent{Kind: "concurrent", Level: "info", DisplayText: fmt.Sprintf("event-%d", index), RawJSON: `{}`})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var total, distinct int
	if err := db.QueryRow(`SELECT COUNT(*),COUNT(DISTINCT sequence) FROM task_events WHERE task_id=?`, task.ID).Scan(&total, &distinct); err != nil {
		t.Fatal(err)
	}
	if total != count || distinct != count {
		t.Fatalf("total=%d distinct_sequences=%d want=%d", total, distinct, count)
	}
}

func assertTaskResultCounts(t *testing.T, db *sql.DB, taskID string, status domain.TaskStatus, messages, events, artifacts, assets int) {
	t.Helper()
	var gotStatus domain.TaskStatus
	if err := db.QueryRow(`SELECT status FROM codex_tasks WHERE id=?`, taskID).Scan(&gotStatus); err != nil {
		t.Fatal(err)
	}
	if gotStatus != status {
		t.Fatalf("status=%s want=%s", gotStatus, status)
	}
	for table, want := range map[string]int{"task_messages": messages, "task_events": events, "task_artifacts": artifacts, "asset_versions": assets} {
		var got int
		query := `SELECT COUNT(*) FROM ` + table
		if table != "asset_versions" {
			query += ` WHERE task_id=?`
		} else {
			query += ` WHERE source_task_id=?`
		}
		if err := db.QueryRow(query, taskID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s=%d want=%d", table, got, want)
		}
	}
}
