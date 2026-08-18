package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

// Hub fans task events out to WebSocket clients. Events are persisted by the
// task runner; the hub is deliberately only a delivery and replay layer.
type Hub struct {
	repo   *store.TaskRepository
	mu     sync.Mutex
	byTask map[string]map[*client]struct{}
	closed bool
}

type client struct {
	conn         *websocket.Conn
	mu           sync.Mutex
	lastSequence int64
	writeEvent   func(context.Context, domain.TaskEvent) error
}

func NewHub(repo *store.TaskRepository) *Hub {
	return &Hub{repo: repo, byTask: make(map[string]map[*client]struct{})}
}

// Publish sends an already-persisted event to all current subscribers.
func (h *Hub) Publish(ctx context.Context, taskID string, event domain.TaskEvent) {
	if taskID == "" {
		return
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	clients := make([]*client, 0, len(h.byTask[taskID]))
	for c := range h.byTask[taskID] {
		clients = append(clients, c)
	}
	h.mu.Unlock()
	for _, c := range clients {
		_ = c.write(ctx, event)
	}
}

func (h *Hub) add(taskID string, c *client) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	if h.byTask[taskID] == nil {
		h.byTask[taskID] = make(map[*client]struct{})
	}
	h.byTask[taskID][c] = struct{}{}
	return true
}
func (h *Hub) remove(taskID string, c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if clients := h.byTask[taskID]; clients != nil {
		delete(clients, c)
		if len(clients) == 0 {
			delete(h.byTask, taskID)
		}
	}
}

func (h *Hub) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	clients := make([]*client, 0)
	for _, taskClients := range h.byTask {
		for c := range taskClients {
			clients = append(clients, c)
		}
	}
	h.byTask = make(map[string]map[*client]struct{})
	h.mu.Unlock()

	for _, c := range clients {
		c.close(websocket.StatusGoingAway, "server shutting down")
	}
}

func (c *client) close(status websocket.StatusCode, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		_ = c.conn.Close(status, reason)
	}
}

func (c *client) write(ctx context.Context, event domain.TaskEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writeLocked(ctx, event)
}

func (c *client) writeLocked(ctx context.Context, event domain.TaskEvent) error {
	if event.Sequence <= c.lastSequence {
		return nil
	}
	if c.writeEvent != nil {
		if err := c.writeEvent(ctx, event); err != nil {
			return err
		}
		c.lastSequence = event.Sequence
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := c.conn.Write(ctx, websocket.MessageText, mustJSON(event)); err != nil {
		return err
	}
	c.lastSequence = event.Sequence
	return nil
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

// Handler serves GET /api/tasks/{id}/events. The optional `after` query
// parameter is an event sequence cursor used for reconnect replay.
func (h *Hub) Handler(w http.ResponseWriter, r *http.Request, taskID string) {
	if h.repo == nil || taskID == "" {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	if _, err := h.repo.Get(r.Context(), taskID); err != nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	if !localOrigin(r) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	after, err := parseAfter(r.URL.Query().Get("after"))
	if err != nil {
		http.Error(w, "invalid after", http.StatusBadRequest)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	c := &client{conn: conn, lastSequence: after}
	// Register while holding the per-client write lock. Concurrent publishes
	// then wait until the persisted replay snapshot has been delivered. The
	// sequence cursor suppresses the duplicate publish for an event that was
	// persisted after registration but was already included in the snapshot.
	c.mu.Lock()
	if !h.add(taskID, c) {
		c.mu.Unlock()
		c.close(websocket.StatusGoingAway, "server shutting down")
		return
	}
	events, err := h.repo.Events(r.Context(), taskID)
	if err != nil {
		h.remove(taskID, c)
		c.mu.Unlock()
		c.close(websocket.StatusInternalError, "event replay failed")
		return
	}
	for _, event := range events {
		if err := c.writeLocked(r.Context(), event); err != nil {
			h.remove(taskID, c)
			c.mu.Unlock()
			return
		}
	}
	c.mu.Unlock()
	defer h.remove(taskID, c)
	for {
		_, _, err := conn.Read(r.Context())
		if err != nil {
			return
		}
	}
}

func parseAfter(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		return 0, errors.New("after must be a non-negative integer")
	}
	return n, nil
}

func localOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	requestHost := r.Host
	if parsed, _, splitErr := net.SplitHostPort(r.Host); splitErr == nil {
		requestHost = parsed
	}
	requestHost = strings.Trim(strings.ToLower(requestHost), "[]")
	return host == requestHost || host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "[::1]"
}
