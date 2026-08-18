package codexapp

import (
	"encoding/json"
	"errors"
	"fmt"
)

type Request struct {
	Method string `json:"method"`
	ID     int64  `json:"id"`
	Params any    `json:"params,omitempty"`
}

type Response struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

type Notification struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return "codex app RPC error"
	}
	return fmt.Sprintf("codex app RPC error %d: %s", e.Code, e.Message)
}

var (
	ErrClosed               = errors.New("codex app client closed")
	ErrFrameTooLarge        = errors.New("codex app JSON payload exceeds 16 MiB")
	ErrInvalidWriteCount    = errors.New("codex app writer returned an invalid byte count")
	ErrNotificationOverflow = errors.New("codex app notification buffer overflow")
)

type ProtocolError struct {
	Message string
	Err     error
}

func (e *ProtocolError) Error() string {
	if e.Err == nil {
		return "codex app protocol error: " + e.Message
	}
	return fmt.Sprintf("codex app protocol error: %s: %v", e.Message, e.Err)
}

func (e *ProtocolError) Unwrap() error {
	return e.Err
}
