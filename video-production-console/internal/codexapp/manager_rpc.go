package codexapp

import (
	"context"
	"errors"
	"sync"
)

// ManagerRPC lazily obtains the Manager-owned Client. It is safe to hand to
// higher-level services: no caller receives process handles or process control.
type ManagerRPC struct {
	manager *Manager
	notify  chan Notification
	once    sync.Once
}

func NewManagerRPC(manager *Manager) *ManagerRPC {
	return &ManagerRPC{manager: manager, notify: make(chan Notification, 256)}
}

func (r *ManagerRPC) Call(ctx context.Context, method string, params any, result any) error {
	if r == nil || r.manager == nil {
		return errors.New("Codex App Server manager is not configured")
	}
	client, err := r.manager.Ensure(ctx)
	if err != nil {
		return err
	}
	r.once.Do(func() { go r.forward(client.Notifications()) })
	return client.Call(ctx, method, params, result)
}

func (r *ManagerRPC) Notifications() <-chan Notification {
	if r == nil {
		return nil
	}
	return r.notify
}

func (r *ManagerRPC) forward(source <-chan Notification) {
	for notification := range source {
		// Do not silently drop a state transition after the Client has accepted
		// it. The Broker owns the consumer for this channel.
		r.notify <- notification
	}
}
