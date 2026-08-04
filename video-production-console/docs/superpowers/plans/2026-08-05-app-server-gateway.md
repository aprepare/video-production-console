# App Server Gateway Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add durable Codex conversations and a console-owned App Server transport that supports start, steer, queue, resume, and fork while preserving every existing CLI task.

**Architecture:** Add conversation tables beside the existing task tables, implement a minimal JSON-RPC client over App Server stdio, and place a Thread Broker between HTTP handlers and the transport. Keep `TaskScheduler` as the `legacy_exec` adapter; transport is frozen when each task is created.

**Tech Stack:** Go 1.25, SQLite, `os/exec`, JSON-RPC over JSONL stdio, existing authentication and WebSocket packages.

---

## File map

- `internal/domain/conversations.go`: conversation, turn, message, delivery, outbox, and lease types.
- `internal/store/conversations.go`: transactional persistence and idempotent outbox.
- `internal/codexapp/protocol.go`, `client.go`, `manager.go`: App Server protocol and owned process.
- `internal/conversation/broker.go`, `service.go`: thread state machine and product operations.
- `internal/httpapi/conversations.go`: REST API.
- `internal/store/migrations.go`, `internal/domain/models.go`, `internal/store/tasks.go`: additive task metadata.
- `internal/app/app.go`, `cmd/console/main.go`: dependency wiring and shutdown.

### Task 1: Add conversation persistence and task transport metadata

**Files:**
- Create: `internal/domain/conversations.go`
- Create: `internal/store/conversations.go`
- Create: `internal/store/conversations_test.go`
- Modify: `internal/store/migrations.go`
- Modify: `internal/store/migrations_test.go`
- Modify: `internal/domain/models.go`
- Modify: `internal/store/tasks.go`
- Modify: `internal/store/tasks_test.go`

- [ ] **Step 1: Write the failing repository test**

```go
func TestConversationOutboxIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	repo := NewConversationRepository(db)
	session := domain.ChatSession{ID: uuid.NewString(), Kind: domain.ChatGeneral, Title: "测试对话", Status: domain.ChatIdle}
	if err := repo.CreateSession(context.Background(), session); err != nil { t.Fatal(err) }
	first, err := repo.Enqueue(context.Background(), session.ID, "client-1", "继续处理", domain.DeliveryAuto)
	if err != nil { t.Fatal(err) }
	second, err := repo.Enqueue(context.Background(), session.ID, "client-1", "继续处理", domain.DeliveryAuto)
	if err != nil { t.Fatal(err) }
	if first.ID != second.ID { t.Fatalf("duplicate rows: %s != %s", first.ID, second.ID) }
}
```

Also extend migration tests to assert `chat_sessions`, `chat_turns`, `chat_messages`, `chat_outbox`, `semantic_events`, and `thread_leases`, plus five new `codex_tasks` columns. The session table must persist `working_directory`, `model`, `reasoning_effort`, and `skill_names_json` so a resumed session never silently changes execution settings.

- [ ] **Step 2: Run the focused tests**

```powershell
go test ./internal/store ./internal/domain -run 'Conversation|Migration|TaskTransport' -count=1
```

Expected: compile failure because the new domain and repository types do not exist.

- [ ] **Step 3: Implement the schema and types**

Use these exact public types:

```go
type ChatKind string
const (ChatGeneral ChatKind = "general"; ChatIdea ChatKind = "idea"; ChatProject ChatKind = "project"; ChatHistory ChatKind = "history")
type DeliveryMode string
const (DeliveryAuto DeliveryMode = "auto"; DeliverySteer DeliveryMode = "steer"; DeliveryQueue DeliveryMode = "queue")
type ChatStatus string
const (ChatIdle ChatStatus = "idle"; ChatRunning ChatStatus = "running"; ChatAwaitingInput ChatStatus = "awaiting_input"; ChatFailed ChatStatus = "failed")

type ChatSession struct {
	ID, Title, Source string
	Kind ChatKind
	Status ChatStatus
	ProjectID, IdeaSessionID, CodexThreadID *string
	WorkingDirectory, Model, ReasoningEffort string
	SkillNames []string
	CreatedAt, UpdatedAt time.Time
}
type ChatMessage struct {
	ID, SessionID, Role, Kind, Content, DeliveryStatus, ClientKey string
	CodexItemID, TurnID *string
	Sequence int64
	CreatedAt time.Time
}
```

Add nullable `chat_session_id`, `codex_thread_id`, and `codex_turn_id`, plus `completion_phase TEXT NOT NULL DEFAULT 'agent_running'` and `transport TEXT NOT NULL DEFAULT 'legacy_exec'`, to `codex_tasks`. Store the four session execution fields above on `chat_sessions`, validate `skill_names_json` as a JSON string array when reading, and update repository scans and tests in the same change. `Enqueue` must use unique `(session_id, client_key)` and return the existing row on conflict.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/store ./internal/domain -count=1
git add internal/domain/conversations.go internal/domain/models.go internal/store/conversations.go internal/store/conversations_test.go internal/store/migrations.go internal/store/migrations_test.go internal/store/tasks.go internal/store/tasks_test.go
git diff --cached --name-only
git commit -m "feat: add durable Codex conversations"
```

### Task 2: Implement the JSON-RPC stdio client

**Files:**
- Create: `internal/codexapp/protocol.go`
- Create: `internal/codexapp/client.go`
- Create: `internal/codexapp/client_test.go`

- [ ] **Step 1: Write correlation and notification tests**

```go
func TestClientCorrelatesResponsesAndStreamsNotifications(t *testing.T) {
	serverR, serverW := io.Pipe()
	clientR, clientW := io.Pipe()
	c := NewClient(serverR, clientW)
	defer c.Close()
	go func() {
		var req Request
		_ = json.NewDecoder(clientR).Decode(&req)
		_ = json.NewEncoder(serverW).Encode(Response{ID: req.ID, Result: json.RawMessage(`{"turnId":"turn-1"}`)})
		_ = json.NewEncoder(serverW).Encode(Notification{Method: "turn/completed", Params: json.RawMessage(`{"turn":{"id":"turn-1"}}`)})
	}()
	var result struct{ TurnID string `json:"turnId"` }
	if err := c.Call(context.Background(), "turn/steer", map[string]any{"threadId":"thread-1"}, &result); err != nil { t.Fatal(err) }
	if result.TurnID != "turn-1" { t.Fatalf("turn=%q", result.TurnID) }
	if got := <-c.Notifications(); got.Method != "turn/completed" { t.Fatalf("method=%q", got.Method) }
}
```

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/codexapp -run TestClient -count=1
```

- [ ] **Step 3: Implement bounded JSONL framing**

```go
type Request struct { Method string `json:"method"`; ID int64 `json:"id"`; Params any `json:"params,omitempty"` }
type Response struct { ID int64 `json:"id"`; Result json.RawMessage `json:"result,omitempty"`; Error *RPCError `json:"error,omitempty"` }
type Notification struct { Method string `json:"method"`; Params json.RawMessage `json:"params,omitempty"` }
type RPCError struct { Code int `json:"code"`; Message string `json:"message"`; Data json.RawMessage `json:"data,omitempty"` }
```

Use one reader goroutine, one write mutex, atomic request IDs, a pending map, and a bounded notification channel. Cap a JSONL frame at 16 MiB. Context cancellation removes the pending call. Malformed JSON closes the client and fails all pending calls.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/codexapp -count=1
git add internal/codexapp/protocol.go internal/codexapp/client.go internal/codexapp/client_test.go
git commit -m "feat: add Codex App Server RPC client"
```

### Task 3: Own exactly one App Server process

**Files:**
- Create: `internal/codexapp/manager.go`
- Create: `internal/codexapp/manager_test.go`
- Create: `internal/codexapp/process_windows.go`
- Create: `internal/codexapp/process_other.go`

- [ ] **Step 1: Write lifecycle tests**

Use these injectable boundaries and assert that two `Start` calls share one process and `Close` terminates only the stored process:

```go
type ProcessFactory interface { Start(context.Context) (ManagedProcess, error) }
type ManagedProcess interface {
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	Stderr() io.ReadCloser
	Wait() error
	Terminate() error
}
```

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/codexapp -run Manager -count=1
```

- [ ] **Step 3: Implement lifecycle and initialization**

The production factory must call:

```go
exec.CommandContext(ctx, codexBinary, "app-server", "--listen", "stdio://", "--strict-config")
```

After start, send `initialize` with `clientInfo.name="video-production-console"`, then the protocol's initialized notification. Store stderr in a bounded ring buffer. An unexpected exit changes health to `failed`; it must not create an unbounded restart loop. `Close` never enumerates or kills unrelated Codex, Node, desktop, or proxy processes.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/codexapp -count=1
git add internal/codexapp/manager.go internal/codexapp/manager_test.go internal/codexapp/process_windows.go internal/codexapp/process_other.go
git commit -m "feat: manage the console App Server process"
```

### Task 4: Implement steering, queueing, and turn races

**Files:**
- Create: `internal/conversation/broker.go`
- Create: `internal/conversation/broker_test.go`

- [ ] **Step 1: Write state-machine tests**

Cover active auto delivery, idle auto delivery, explicit steer conflict, and a queued message that starts exactly once after `turn/completed`.

```go
func TestAutoDeliverySteersActiveTurn(t *testing.T) {
	rpc := &fakeRPC{activeTurn: "turn-7"}
	broker := NewBroker(repo, rpc)
	receipt, err := broker.Send(ctx, SendInput{SessionID: sessionID, ClientKey: "m1", Text: "先处理失败测试", Delivery: domain.DeliveryAuto})
	if err != nil { t.Fatal(err) }
	if receipt.Method != "turn/steer" || receipt.TurnID != "turn-7" { t.Fatalf("receipt=%+v", receipt) }
	if rpc.lastParams["expectedTurnId"] != "turn-7" { t.Fatalf("params=%v", rpc.lastParams) }
}
```

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/conversation -run 'AutoDelivery|QueuedDelivery|SteerConflict' -count=1
```

- [ ] **Step 3: Implement the Broker contract**

```go
type RPC interface { Call(context.Context, string, any, any) error; Notifications() <-chan codexapp.Notification }
type SendInput struct { SessionID, ClientKey, Text string; Delivery domain.DeliveryMode }
type SendReceipt struct { MessageID, Method, ThreadID, TurnID, State string }
```

Acquire `thread_leases` before mutating a thread. `auto` uses `turn/steer` when a turn is active and `turn/start` otherwise. Include `expectedTurnId` in steer. On a stale-turn response, refresh once: auto may become a new turn, while explicit steer remains unsent. Persist `sending`, `accepted`, `queued`, and `failed` before broadcasting.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/conversation ./internal/store -count=1
git add internal/conversation/broker.go internal/conversation/broker_test.go
git commit -m "feat: route Codex steering and queued messages"
```

### Task 5: Expose conversation operations

**Files:**
- Create: `internal/conversation/service.go`
- Create: `internal/conversation/service_test.go`
- Create: `internal/httpapi/conversations.go`
- Create: `internal/httpapi/conversations_test.go`

- [ ] **Step 1: Write API tests**

```go
request := httptest.NewRequest(http.MethodPost, "/api/chat/sessions/"+sessionID+"/messages", strings.NewReader(`{"text":"继续","delivery":"auto","client_key":"k1"}`))
response := httptest.NewRecorder()
handler.ServeHTTP(response, request)
if response.Code != http.StatusAccepted { t.Fatalf("status=%d body=%s", response.Code, response.Body.String()) }
```

Test create, list, get, delete mapping, message delivery, and fork. Reject malformed UUIDs, unknown sessions, messages over 64 KiB, and arbitrary thread IDs supplied to the message endpoint.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/httpapi ./internal/conversation -run Conversation -count=1
```

- [ ] **Step 3: Implement endpoints**

Mount exactly:

```text
GET    /api/chat/sessions
POST   /api/chat/sessions
GET    /api/chat/sessions/{id}
DELETE /api/chat/sessions/{id}
POST   /api/chat/sessions/{id}/messages
POST   /api/chat/sessions/{id}/fork
```

Create uses this server-validated input:

```go
type CreateSessionInput struct {
	Kind domain.ChatKind `json:"kind"`
	Title, WorkingDirectory, Model, ReasoningEffort string
	SkillNames []string
	ProjectID, IdeaSessionID *string
}
```

`WorkingDirectory` must equal or be contained by one of the configured canonical `codex_workspace_roots`; general chat defaults to the first root. Resolve `SkillNames` through stored Skill snapshots, and resolve model/reasoning through the existing model-selection validator. The browser cannot submit a Skill path or an arbitrary filesystem root.

Create uses `thread/start`; mapping an existing thread uses `thread/resume`; fork uses `thread/fork`. Deleting a console session removes only the mapping unless archive is explicitly requested.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/httpapi ./internal/conversation -count=1
git add internal/conversation/service.go internal/conversation/service_test.go internal/httpapi/conversations.go internal/httpapi/conversations_test.go
git commit -m "feat: expose Codex conversation APIs"
```

### Task 6: Preserve the legacy scheduler and freeze transport per task

**Files:**
- Modify: `internal/codex/scheduler.go`
- Modify: `internal/codex/scheduler_test.go`
- Modify: `internal/httpapi/tasks.go`
- Modify: `internal/httpapi/tasks_test.go`
- Modify: `internal/httpapi/ideas.go`
- Modify: `internal/httpapi/ideas_test.go`

- [ ] **Step 1: Write compatibility tests**

Create one `legacy_exec` task and one `app_server` task. Assert the legacy task invokes `CommandFactory`, the App Server task invokes Broker, and changing the global feature flag does not change either persisted transport.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/codex ./internal/httpapi -run 'LegacyTransport|AppServerTransport|FrozenTransport' -count=1
```

- [ ] **Step 3: Add a composite dispatcher**

```go
type TaskDispatcher interface {
	Enqueue(context.Context, domain.CodexTask) error
	Resume(context.Context, string, string) error
	Cancel(context.Context, string) error
}
```

Keep `TaskScheduler` implementing the interface for legacy tasks. Add a composite dispatcher that selects only from `task.Transport`. New task creation resolves `app_server_enabled`, persists the transport, and attaches the project's main chat session. Resume never re-resolves transport.

For App Server tasks, `Cancel` calls `turn/interrupt` with the persisted thread and active turn IDs. It may never terminate the shared App Server process. A task with no active turn returns a conflict instead of interrupting another thread.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/codex ./internal/httpapi -count=1
git add internal/codex/scheduler.go internal/codex/scheduler_test.go internal/httpapi/tasks.go internal/httpapi/tasks_test.go internal/httpapi/ideas.go internal/httpapi/ideas_test.go
git commit -m "feat: dispatch tasks by frozen Codex transport"
```

### Task 7: Wire settings, health, and graceful shutdown

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`
- Modify: `cmd/console/main.go`
- Modify: `cmd/console/main_test.go`
- Modify: `internal/domain/settings.go`
- Modify: `internal/settings/service.go`
- Modify: `internal/settings/service_test.go`
- Modify: `schemas/settings.schema.json`
- Modify: `schemas/settings_schema_test.go`

- [ ] **Step 1: Write settings and runtime tests**

Add `app_server_enabled` with default `false` during migration and `codex_workspace_roots` as a JSON array of canonical absolute directories. Default the roots to the console repository working directory and data root, deduplicated after canonicalization. Assert `/api/runtime` reports sanitized App Server health and transport counts without credentials or environment values.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/app ./internal/settings ./cmd/console ./schemas -run 'AppServer|Runtime' -count=1
```

- [ ] **Step 3: Wire production dependencies**

Extend `app.Options` with Conversation Service and App Server health. In `main`, construct Manager, Broker, Service, and the composite dispatcher after database/settings initialization. Defer Manager close after Scheduler close. Start App Server lazily when the feature is enabled and a conversation is requested.

Use this response shape:

```go
type AppServerHealth struct { Status string `json:"status"`; PID int `json:"pid,omitempty"`; LastError string `json:"last_error,omitempty"` }
```

Pass `LastError` through the existing redactor.

- [ ] **Step 4: Run backend verification and commit**

```powershell
go test ./...
go vet ./...
git add internal/app/app.go internal/app/app_test.go cmd/console/main.go cmd/console/main_test.go internal/domain/settings.go internal/settings/service.go internal/settings/service_test.go schemas/settings.schema.json schemas/settings_schema_test.go
git commit -m "feat: wire the App Server conversation gateway"
```

Expected: all Go tests and vet pass; tests do not start or stop any real Codex desktop process.
