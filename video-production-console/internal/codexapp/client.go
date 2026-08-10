package codexapp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

const (
	// maxFrameSize is the maximum JSON payload size. The JSONL newline is not
	// included in this limit.
	maxFrameSize              = 16 << 20
	notificationChannelBuffer = 64

	writeStateWriting uint32 = iota
	writeStateCompleted
	writeStateCanceled
)

type callResult struct {
	result json.RawMessage
	err    error
}

type Client struct {
	reader *bufio.Reader
	writer io.Writer

	nextID atomic.Int64
	write  sync.Mutex

	state         sync.Mutex
	pending       map[int64]chan callResult
	notifications chan Notification
	terminalErr   error
	done          chan struct{}
	terminated    sync.Once

	closeTransport func() error
	transportOnce  sync.Once
	closeOnce      sync.Once
	closeErr       error

	afterWriteCompleted     func()
	afterResponseDispatched func()
	beforeResponseWait      func()
}

// NewClient creates a non-owning client. It never closes reader or writer.
// Because cancellation cannot unblock an arbitrary non-owned Writer, this
// constructor is suitable only for in-memory I/O that cannot block forever.
// Production process and pipe integrations must use NewClientWithClose.
func NewClient(reader io.Reader, writer io.Writer) *Client {
	return newClient(reader, writer, nil)
}

// NewClientWithClose creates a client that owns one transport close function.
// The function must unblock both reader and writer and is invoked at most once
// when the client closes, encounters a protocol failure, or cancels a blocked
// write.
func NewClientWithClose(reader io.Reader, writer io.Writer, closeTransport func() error) *Client {
	return newClient(reader, writer, closeTransport)
}

func newClient(reader io.Reader, writer io.Writer, closeTransport func() error) *Client {
	client := &Client{
		reader:         bufio.NewReader(reader),
		writer:         writer,
		pending:        make(map[int64]chan callResult),
		notifications:  make(chan Notification, notificationChannelBuffer),
		done:           make(chan struct{}),
		closeTransport: closeTransport,
	}
	go client.readLoop()
	return client
}

func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	id := c.nextID.Add(1)
	payload, err := json.Marshal(Request{Method: method, ID: id, Params: params})
	if err != nil {
		return fmt.Errorf("encode codex app request: %w", err)
	}
	if len(payload) > maxFrameSize {
		return &ProtocolError{Message: "request JSON payload too large", Err: ErrFrameTooLarge}
	}
	frame := append(payload, '\n')

	response := make(chan callResult, 1)
	c.state.Lock()
	if c.terminalErr != nil {
		err := c.terminalErr
		c.state.Unlock()
		return err
	}
	c.pending[id] = response
	c.state.Unlock()

	writeDone := make(chan error, 1)
	var writeState atomic.Uint32
	writeState.Store(writeStateWriting)
	go func() {
		err := c.writeFrame(frame)
		if writeState.CompareAndSwap(writeStateWriting, writeStateCompleted) && c.afterWriteCompleted != nil {
			c.afterWriteCompleted()
		}
		writeDone <- err
	}()

	select {
	case err := <-writeDone:
		if err != nil {
			c.removePending(id, response)
			return err
		}
	case <-ctx.Done():
		if !c.removePending(id, response) {
			return c.awaitClaimedResult(response, result)
		}
		if writeState.CompareAndSwap(writeStateWriting, writeStateCanceled) {
			if c.closeTransport != nil {
				c.terminate(fmt.Errorf("codex app request write canceled: %w", ctx.Err()))
			}
			return ctx.Err()
		}
		return ctx.Err()
	case <-c.done:
		return c.resultAfterDone(id, response, result)
	}
	if c.beforeResponseWait != nil {
		c.beforeResponseWait()
	}

	select {
	case reply := <-response:
		return finishCallResult(reply, result)
	case <-ctx.Done():
		if c.removePending(id, response) {
			return ctx.Err()
		}
		return c.awaitClaimedResult(response, result)
	case <-c.done:
		return c.resultAfterDone(id, response, result)
	}
}

// Notify sends a JSON-RPC notification through the same serialized, bounded
// transport used by Call. Notifications intentionally have no request ID.
func (c *Client) Notify(ctx context.Context, method string, params any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	payload, err := json.Marshal(struct {
		Method string `json:"method"`
		Params any    `json:"params,omitempty"`
	}{Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("encode codex app notification: %w", err)
	}
	if len(payload) > maxFrameSize {
		return &ProtocolError{Message: "notification JSON payload too large", Err: ErrFrameTooLarge}
	}
	frame := append(payload, '\n')

	writeDone := make(chan error, 1)
	var state atomic.Uint32
	state.Store(writeStateWriting)
	go func() {
		err := c.writeFrame(frame)
		if state.CompareAndSwap(writeStateWriting, writeStateCompleted) && c.afterWriteCompleted != nil {
			c.afterWriteCompleted()
		}
		writeDone <- err
	}()

	select {
	case err := <-writeDone:
		return err
	case <-ctx.Done():
		if state.CompareAndSwap(writeStateWriting, writeStateCanceled) {
			c.terminate(fmt.Errorf("codex app notification write canceled: %w", ctx.Err()))
			return ctx.Err()
		}
		return <-writeDone
	case <-c.done:
		return c.getTerminalError()
	}
}

func (c *Client) Notifications() <-chan Notification {
	return c.notifications
}

func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.terminate(ErrClosed)
		c.closeOwnedTransport()
		c.state.Lock()
		c.terminalErr = ErrClosed
		c.state.Unlock()
	})
	return c.closeErr
}

func (c *Client) writeFrame(frame []byte) error {
	c.write.Lock()
	defer c.write.Unlock()

	if err := c.getTerminalError(); err != nil {
		return err
	}

	for len(frame) > 0 {
		if err := c.getTerminalError(); err != nil {
			return err
		}
		written, err := c.writer.Write(frame)
		if written < 0 || written > len(frame) {
			writeErr := fmt.Errorf("%w: wrote %d bytes with %d remaining", ErrInvalidWriteCount, written, len(frame))
			c.terminate(writeErr)
			return writeErr
		}
		if err != nil {
			writeErr := fmt.Errorf("write codex app request: %w", err)
			c.terminate(writeErr)
			return writeErr
		}
		if written == 0 {
			writeErr := fmt.Errorf("write codex app request: %w", io.ErrShortWrite)
			c.terminate(writeErr)
			return writeErr
		}
		frame = frame[written:]
	}
	return nil
}

func (c *Client) readLoop() {
	for {
		frame, err := c.readFrame()
		if err != nil {
			c.terminate(err)
			return
		}
		if err := c.dispatchFrame(frame); err != nil {
			c.terminate(err)
			return
		}
	}
}

func (c *Client) readFrame() ([]byte, error) {
	frame := make([]byte, 0, 1024)
	for {
		part, isPrefix, err := c.reader.ReadLine()
		if len(frame)+len(part) > maxFrameSize {
			return nil, &ProtocolError{Message: "JSON payload too large", Err: ErrFrameTooLarge}
		}
		frame = append(frame, part...)
		if err != nil {
			return nil, err
		}
		if !isPrefix {
			return frame, nil
		}
	}
}

func (c *Client) dispatchFrame(frame []byte) error {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(frame, &envelope); err != nil {
		return &ProtocolError{Message: "malformed JSON frame", Err: err}
	}
	if envelope == nil {
		return &ProtocolError{Message: "frame is not an object"}
	}

	_, hasID := envelope["id"]
	_, hasMethod := envelope["method"]
	switch {
	case hasID && hasMethod:
		// App Server can send JSON-RPC requests to its client (for example an
		// approval or auth refresh request). A client that only understands
		// responses must still answer these requests; treating them as malformed
		// would tear down the whole transport during initialization.
		return c.dispatchServerRequest(envelope)
	case hasID:
		return c.dispatchResponse(envelope)
	case hasMethod:
		return c.dispatchNotification(envelope)
	default:
		return &ProtocolError{Message: "unknown envelope contains neither id nor method"}
	}
}

func (c *Client) dispatchServerRequest(envelope map[string]json.RawMessage) error {
	var method string
	if err := json.Unmarshal(envelope["method"], &method); err != nil || method == "" {
		return &ProtocolError{Message: "server request method is invalid"}
	}
	response := map[string]any{
		"id": envelope["id"],
		"error": map[string]any{
			"code":    -32601,
			"message": "console does not handle server request " + method,
		},
	}
	payload, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("encode codex app server response: %w", err)
	}
	return c.writeFrame(append(payload, '\n'))
}

func (c *Client) dispatchResponse(envelope map[string]json.RawMessage) error {
	resultPayload, hasResult := envelope["result"]
	errorPayload, hasError := envelope["error"]
	if hasResult == hasError {
		return &ProtocolError{Message: "response must contain exactly one of result or error"}
	}

	var id int64
	if bytes.Equal(bytes.TrimSpace(envelope["id"]), []byte("null")) {
		return &ProtocolError{Message: "response id is null"}
	}
	if err := json.Unmarshal(envelope["id"], &id); err != nil {
		return &ProtocolError{Message: "response id is not an integer", Err: err}
	}

	reply := callResult{result: resultPayload}
	if hasError {
		var rpcErr *RPCError
		if err := json.Unmarshal(errorPayload, &rpcErr); err != nil {
			return &ProtocolError{Message: "malformed RPC error", Err: err}
		}
		if rpcErr == nil {
			return &ProtocolError{Message: "RPC error is null"}
		}
		reply.err = rpcErr
	}

	c.state.Lock()
	pending := c.pending[id]
	if pending != nil {
		pending <- reply
		delete(c.pending, id)
	}
	c.state.Unlock()
	if pending != nil && c.afterResponseDispatched != nil {
		c.afterResponseDispatched()
	}
	return nil
}

func (c *Client) awaitClaimedResult(response <-chan callResult, result any) error {
	select {
	case reply := <-response:
		return finishCallResult(reply, result)
	case <-c.done:
		select {
		case reply := <-response:
			return finishCallResult(reply, result)
		default:
			return c.getTerminalError()
		}
	}
}

func (c *Client) resultAfterDone(id int64, response chan callResult, result any) error {
	if c.removePending(id, response) {
		return c.getTerminalError()
	}
	return c.awaitClaimedResult(response, result)
}

func finishCallResult(reply callResult, result any) error {
	if reply.err != nil {
		return reply.err
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(reply.result, result); err != nil {
		return fmt.Errorf("decode codex app response result: %w", err)
	}
	return nil
}

func (c *Client) dispatchNotification(envelope map[string]json.RawMessage) error {
	if _, ok := envelope["result"]; ok {
		return &ProtocolError{Message: "notification contains result"}
	}
	if _, ok := envelope["error"]; ok {
		return &ProtocolError{Message: "notification contains error"}
	}

	var notification Notification
	if err := json.Unmarshal(envelope["method"], &notification.Method); err != nil {
		return &ProtocolError{Message: "notification method is not a string", Err: err}
	}
	if notification.Method == "" {
		return &ProtocolError{Message: "notification method is empty"}
	}
	if params, ok := envelope["params"]; ok {
		notification.Params = append(json.RawMessage(nil), params...)
	}

	c.state.Lock()
	if c.terminalErr != nil {
		c.state.Unlock()
		return nil
	}
	select {
	case c.notifications <- notification:
		c.state.Unlock()
		return nil
	default:
		c.state.Unlock()
		return &ProtocolError{Message: "notification buffer is full", Err: ErrNotificationOverflow}
	}
}

func (c *Client) removePending(id int64, response chan callResult) bool {
	c.state.Lock()
	defer c.state.Unlock()
	if c.pending[id] != response {
		return false
	}
	delete(c.pending, id)
	return true
}

func (c *Client) getTerminalError() error {
	c.state.Lock()
	defer c.state.Unlock()
	return c.terminalErr
}

func (c *Client) terminate(err error) {
	if err == nil {
		err = ErrClosed
	}
	terminatedNow := false
	c.terminated.Do(func() {
		terminatedNow = true
		c.state.Lock()
		c.terminalErr = err
		pending := c.pending
		for _, response := range pending {
			response <- callResult{err: err}
		}
		c.pending = make(map[int64]chan callResult)
		close(c.done)
		close(c.notifications)
		c.state.Unlock()
	})
	if terminatedNow {
		go c.closeOwnedTransport()
	}
}

func (c *Client) closeOwnedTransport() {
	c.transportOnce.Do(func() {
		if c.closeTransport != nil {
			c.closeErr = c.closeTransport()
		}
	})
}
