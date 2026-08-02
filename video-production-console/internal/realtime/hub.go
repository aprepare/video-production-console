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
}

type client struct {
	conn *websocket.Conn
	mu   sync.Mutex
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
	clients := make([]*client, 0, len(h.byTask[taskID]))
	for c := range h.byTask[taskID] {
		clients = append(clients, c)
	}
	h.mu.Unlock()
	for _, c := range clients {
		_ = c.write(ctx, event)
	}
}

func (h *Hub) add(taskID string, c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.byTask[taskID] == nil {
		h.byTask[taskID] = make(map[*client]struct{})
	}
	h.byTask[taskID][c] = struct{}{}
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

func (c *client) write(ctx context.Context, event domain.TaskEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.conn.Write(ctx, websocket.MessageText, mustJSON(event))
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
	events, err := h.repo.Events(r.Context(), taskID)
	if err != nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	c := &client{conn: conn}
	// Replay is sent before registration, so a new event cannot overtake the
	// cursor snapshot. The runner persists events before publishing them.
	for _, event := range events {
		if event.Sequence > after {
			if err := c.write(r.Context(), event); err != nil {
				return
			}
		}
	}
	h.add(taskID, c)
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
