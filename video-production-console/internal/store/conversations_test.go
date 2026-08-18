package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
)

func TestConversationOutboxIsIdempotent(t *testing.T) {
	db, err := Open(t.TempDir() + "/conversation.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := NewConversationRepository(db)
	session := domain.ChatSession{
		ID: uuid.NewString(), Kind: domain.ChatGeneral, Title: "测试对话", Status: domain.ChatIdle,
		WorkingDirectory: `C:\workspace`, Model: "gpt-5.6-sol", ReasoningEffort: "medium",
		SkillNames: []string{"finance-topic-selector"},
	}
	if err := repo.CreateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	first, err := repo.Enqueue(context.Background(), session.ID, "client-1", "继续处理", domain.DeliveryAuto)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.Enqueue(context.Background(), session.ID, "client-1", "different payload", domain.DeliveryQueue)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("duplicate rows: %s != %s", first.ID, second.ID)
	}
	if second.Content != first.Content || second.Delivery != first.Delivery {
		t.Fatalf("idempotent result changed payload: first=%+v second=%+v", first, second)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM chat_outbox WHERE session_id=? AND client_key=?`, session.ID, "client-1").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("outbox rows=%d, want 1", count)
	}
}

func TestConversationOutboxConcurrentClientKeyIsIdempotent(t *testing.T) {
	db, err := Open(t.TempDir() + "/concurrent-outbox.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := NewConversationRepository(db)
	session := domain.ChatSession{ID: uuid.NewString(), Title: "chat", Kind: domain.ChatGeneral, Status: domain.ChatIdle}
	if err := repo.CreateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	const workers = 8
	start := make(chan struct{})
	results := make(chan domain.ChatOutbox, workers)
	errs := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			outbox, err := repo.Enqueue(context.Background(), session.ID, "same-key", "payload", domain.DeliveryAuto)
			results <- outbox
			errs <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var firstID string
	for outbox := range results {
		if firstID == "" {
			firstID = outbox.ID
		} else if outbox.ID != firstID {
			t.Fatalf("concurrent enqueue IDs differ: %q != %q", outbox.ID, firstID)
		}
	}
}

func TestConversationMessageSequenceIsMonotonicAcrossClientKeys(t *testing.T) {
	db, err := Open(t.TempDir() + "/message-sequence.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := NewConversationRepository(db)
	session := domain.ChatSession{ID: uuid.NewString(), Title: "chat", Kind: domain.ChatGeneral, Status: domain.ChatIdle}
	if err := repo.CreateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"one", "two", "three"} {
		if _, err := repo.Enqueue(context.Background(), session.ID, key, key, domain.DeliveryAuto); err != nil {
			t.Fatal(err)
		}
	}
	messages, err := repo.ListMessages(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 || messages[0].Sequence != 1 || messages[1].Sequence != 2 || messages[2].Sequence != 3 {
		t.Fatalf("message sequences=%+v", messages)
	}
}

func TestConversationEnqueueRollsBackMessageWhenOutboxWriteFails(t *testing.T) {
	db, err := Open(t.TempDir() + "/outbox-rollback.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := NewConversationRepository(db)
	session := domain.ChatSession{ID: uuid.NewString(), Title: "chat", Kind: domain.ChatGeneral, Status: domain.ChatIdle}
	if err := repo.CreateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_chat_outbox BEFORE INSERT ON chat_outbox BEGIN SELECT RAISE(ABORT,'reject outbox'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Enqueue(context.Background(), session.ID, "rollback", "payload", domain.DeliveryAuto); err == nil {
		t.Fatal("expected outbox insert failure")
	}
	for _, table := range []string{"chat_messages", "chat_outbox"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE session_id=?`, session.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s rows=%d after rollback", table, count)
		}
	}
}

func TestConversationDeleteMappingPreservesTaskAndClearsReference(t *testing.T) {
	db, err := Open(t.TempDir() + "/delete-mapping.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('account','A','#fff','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	repo := NewConversationRepository(db)
	session := domain.ChatSession{ID: uuid.NewString(), Title: "chat", Kind: domain.ChatGeneral, Status: domain.ChatIdle}
	if err := repo.CreateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO codex_tasks(id,account_id,type,skill_name,status,prompt_snapshot,chat_session_id,created_at) VALUES('task','account','chat','skill','completed','prompt',?,?)`, session.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteMapping(context.Background(), session.ID); err != nil {
		t.Fatal(err)
	}
	var taskCount int
	var chatSessionID *string
	if err := db.QueryRow(`SELECT COUNT(*),MAX(chat_session_id) FROM codex_tasks WHERE id='task'`).Scan(&taskCount, &chatSessionID); err != nil {
		t.Fatal(err)
	}
	if taskCount != 1 || chatSessionID != nil {
		t.Fatalf("task count=%d chat_session_id=%v", taskCount, chatSessionID)
	}
}

func TestConversationStatusTransitionsRejectStaleWorkers(t *testing.T) {
	db, err := Open(t.TempDir() + "/status-transition.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := NewConversationRepository(db)
	session := domain.ChatSession{ID: uuid.NewString(), Title: "chat", Kind: domain.ChatGeneral, Status: domain.ChatIdle}
	if err := repo.CreateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	outbox, err := repo.Enqueue(context.Background(), session.ID, "key", "payload", domain.DeliveryAuto)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateOutboxStatus(context.Background(), outbox.ID, domain.ChatOutboxPending, domain.ChatOutboxSending, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateOutboxStatus(context.Background(), outbox.ID, domain.ChatOutboxPending, domain.ChatOutboxSending, nil, nil); err == nil {
		t.Fatal("expected stale outbox transition to fail")
	}
	turn := domain.ChatTurn{ID: uuid.NewString(), SessionID: session.ID, Status: domain.ChatTurnIdle, Delivery: domain.DeliveryAuto}
	if err := repo.CreateTurn(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateTurnStatus(context.Background(), turn.ID, domain.ChatTurnIdle, domain.ChatTurnRunning, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateTurnStatus(context.Background(), turn.ID, domain.ChatTurnIdle, domain.ChatTurnRunning, nil, nil, nil); err == nil {
		t.Fatal("expected stale turn transition to fail")
	}
}

func TestConversationSessionRoundTripsExecutionSettings(t *testing.T) {
	db, err := Open(t.TempDir() + "/session.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := NewConversationRepository(db)
	now := time.Now().UTC().Truncate(time.Millisecond)
	session := domain.ChatSession{
		ID: uuid.NewString(), Title: "Project chat", Source: "console", Kind: domain.ChatProject, Status: domain.ChatRunning,
		WorkingDirectory: `C:\workspace\project`, Model: "openai/custom", ReasoningEffort: "high",
		SkillNames: []string{"one", "two"}, CreatedAt: now, UpdatedAt: now,
	}
	if err := repo.CreateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetSession(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkingDirectory != session.WorkingDirectory || got.Model != session.Model || got.ReasoningEffort != session.ReasoningEffort || len(got.SkillNames) != 2 || got.SkillNames[1] != "two" {
		t.Fatalf("session=%+v", got)
	}
}

func TestConversationSessionRejectsMalformedSkillNames(t *testing.T) {
	db, err := Open(t.TempDir() + "/invalid-skills.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := NewConversationRepository(db)
	session := domain.ChatSession{ID: uuid.NewString(), Title: "chat", Kind: domain.ChatGeneral, Status: domain.ChatIdle}
	if err := repo.CreateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE chat_sessions SET skill_names_json='["valid", 7]' WHERE id=?`, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetSession(context.Background(), session.ID); err == nil {
		t.Fatal("expected non-string skill_names_json member to be rejected")
	}
}

func TestCompletionInboxClaimsRetriesAndCompletesDurably(t *testing.T) {
	db, err := Open(t.TempDir() + "/completion-inbox.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := NewConversationRepository(db)
	session := domain.ChatSession{ID: uuid.NewString(), Title: "chat", Kind: domain.ChatGeneral, Status: domain.ChatRunning}
	if err := repo.CreateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if err := repo.EnqueueCompletion(context.Background(), session.ID, "turn-inbox"); err != nil {
		t.Fatal(err)
	}
	if err := repo.EnqueueCompletion(context.Background(), session.ID, "turn-inbox"); err != nil {
		t.Fatal(err)
	}
	item, claimed, err := repo.ClaimCompletion(context.Background(), true)
	if err != nil || !claimed || item.Attempts != 1 {
		t.Fatalf("claim=%+v claimed=%v err=%v", item, claimed, err)
	}
	if _, claimed, err := repo.ClaimCompletion(context.Background(), true); err != nil || claimed {
		t.Fatalf("duplicate claim=%v err=%v", claimed, err)
	}
	if err := repo.RetryCompletion(context.Background(), item.ID, context.DeadlineExceeded, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	item, claimed, err = repo.ClaimCompletion(context.Background(), true)
	if err != nil || !claimed || item.Attempts != 2 {
		t.Fatalf("retry claim=%+v claimed=%v err=%v", item, claimed, err)
	}
	if err := repo.CompleteCompletion(context.Background(), item.ID); err != nil {
		t.Fatal(err)
	}
	got, err := repo.CompletionForTurn(context.Background(), "turn-inbox")
	if err != nil || got.Status != domain.ChatCompletionDone || got.CompletedAt == nil {
		t.Fatalf("completion=%+v err=%v", got, err)
	}
}

func TestResetProcessingCompletionsMakesCrashClaimReplayable(t *testing.T) {
	db, err := Open(t.TempDir() + "/completion-reset.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := NewConversationRepository(db)
	session := domain.ChatSession{ID: uuid.NewString(), Title: "chat", Kind: domain.ChatGeneral, Status: domain.ChatRunning}
	if err := repo.CreateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if err := repo.EnqueueCompletion(context.Background(), session.ID, "turn-crash"); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := repo.ClaimCompletion(context.Background(), true); err != nil || !claimed {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	if err := repo.ResetProcessingCompletions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := repo.ClaimCompletion(context.Background(), true); err != nil || !claimed {
		t.Fatalf("recovered claim=%v err=%v", claimed, err)
	}
}

func TestCompletionInboxWaitsForFormalTaskHandler(t *testing.T) {
	db, err := Open(t.TempDir() + "/completion-formal-handler.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	repo := NewConversationRepository(db)
	sessionID, threadID, turnID, taskID := uuid.NewString(), "thread-handler", "turn-handler", uuid.NewString()
	if err := repo.CreateSession(context.Background(), domain.ChatSession{ID: sessionID, Title: "formal", Kind: domain.ChatGeneral, Status: domain.ChatRunning, CodexThreadID: &threadID}); err != nil {
		t.Fatal(err)
	}
	task := domain.CodexTask{ID: taskID, AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixStandard, Status: domain.TaskRunning, ChatSessionID: &sessionID, CodexThreadID: &threadID, CodexTurnID: &turnID, Transport: "app_server", CompletionPhase: string(domain.CompletionAgentRunning), PromptSnapshot: "prompt", CreatedAt: now}
	if err := NewTaskRepository(db).CreateV2(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if err := repo.EnqueueCompletion(context.Background(), sessionID, turnID); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := repo.ClaimCompletion(context.Background(), false); err != nil || claimed {
		t.Fatalf("claim without formal handler=%v err=%v", claimed, err)
	}
	if _, claimed, err := repo.ClaimCompletion(context.Background(), true); err != nil || !claimed {
		t.Fatalf("claim with formal handler=%v err=%v", claimed, err)
	}
}
