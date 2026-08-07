package codexapp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type pipeServer struct {
	reader *bufio.Reader
	writer *io.PipeWriter
}

type recordingWriter struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	written chan struct{}
}

type blockingWriter struct {
	started    chan struct{}
	release    chan struct{}
	closed     chan struct{}
	startOnce  sync.Once
	closeOnce  sync.Once
	closeCalls atomic.Int32
}

func newBlockingWriter() *blockingWriter {
	return &blockingWriter{
		started: make(chan struct{}),
		release: make(chan struct{}),
		closed:  make(chan struct{}),
	}
}

func (w *blockingWriter) Write([]byte) (int, error) {
	w.startOnce.Do(func() { close(w.started) })
	<-w.release
	return 0, io.ErrClosedPipe
}

func (w *blockingWriter) Close() error {
	w.closeCalls.Add(1)
	w.closeOnce.Do(func() {
		close(w.release)
		close(w.closed)
	})
	return nil
}

type invalidCountWriter struct {
	count func(int) int
}

func (w invalidCountWriter) Write(payload []byte) (int, error) {
	return w.count(len(payload)), nil
}

func newRecordingWriter() *recordingWriter {
	return &recordingWriter{written: make(chan struct{}, 1)}
}

func (w *recordingWriter) Write(payload []byte) (int, error) {
	w.mu.Lock()
	written, err := w.buffer.Write(payload)
	w.mu.Unlock()
	select {
	case w.written <- struct{}{}:
	default:
	}
	return written, err
}

func (w *recordingWriter) bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.buffer.Bytes()...)
}

func requestParamsForPayloadSize(t *testing.T, method string, id int64, target int) string {
	t.Helper()
	base, err := json.Marshal(Request{Method: method, ID: id, Params: ""})
	if err != nil {
		t.Fatalf("marshal request size baseline: %v", err)
	}
	fillerLength := target - len(base)
	if fillerLength < 0 {
		t.Fatalf("target payload size %d is smaller than baseline %d", target, len(base))
	}
	return strings.Repeat("x", fillerLength)
}

func notificationPayloadForSize(t *testing.T, target int) []byte {
	t.Helper()
	base, err := json.Marshal(map[string]any{"method": "boundary", "params": ""})
	if err != nil {
		t.Fatalf("marshal notification size baseline: %v", err)
	}
	fillerLength := target - len(base)
	if fillerLength < 0 {
		t.Fatalf("target notification size %d is smaller than baseline %d", target, len(base))
	}
	payload, err := json.Marshal(map[string]any{"method": "boundary", "params": strings.Repeat("x", fillerLength)})
	if err != nil {
		t.Fatalf("marshal sized notification: %v", err)
	}
	return payload
}

func newPipeClient(t *testing.T) (*Client, *pipeServer) {
	t.Helper()
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()
	client := NewClient(clientReader, clientWriter)
	t.Cleanup(func() {
		_ = client.Close()
		_ = serverReader.Close()
		_ = serverWriter.Close()
	})
	return client, &pipeServer{reader: bufio.NewReader(serverReader), writer: serverWriter}
}

func (s *pipeServer) readRequest() (Request, error) {
	line, err := s.reader.ReadBytes('\n')
	if err != nil {
		return Request{}, fmt.Errorf("read request: %w", err)
	}
	var request Request
	if err := json.Unmarshal(bytes.TrimSpace(line), &request); err != nil {
		return Request{}, fmt.Errorf("decode request: %w", err)
	}
	return request, nil
}

func (s *pipeServer) writeJSON(value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal server frame: %w", err)
	}
	payload = append(payload, '\n')
	if _, err := s.writer.Write(payload); err != nil {
		return fmt.Errorf("write server frame: %w", err)
	}
	return nil
}

func mustReadRequest(t *testing.T, server *pipeServer) Request {
	t.Helper()
	type outcome struct {
		request Request
		err     error
	}
	done := make(chan outcome, 1)
	go func() {
		request, err := server.readRequest()
		done <- outcome{request: request, err: err}
	}()
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		return result.request
	case <-time.After(time.Second):
		t.Fatal("timed out reading client request")
		return Request{}
	}
}

func mustWriteJSON(t *testing.T, server *pipeServer, value any) {
	t.Helper()
	if err := writeJSONWithTimeout(server, value); err != nil {
		t.Fatal(err)
	}
}

func writeJSONWithTimeout(server *pipeServer, value any) error {
	done := make(chan error, 1)
	go func() {
		done <- server.writeJSON(value)
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		return errors.New("timed out writing server frame")
	}
}

func TestClientCorrelatesResponsesAndStreamsNotifications(t *testing.T) {
	client, server := newPipeClient(t)

	serverDone := make(chan error, 1)
	go func() {
		request, err := server.readRequest()
		if err != nil {
			serverDone <- err
			return
		}
		if request.Method != "turn/steer" {
			serverDone <- fmt.Errorf("method = %q, want turn/steer", request.Method)
			return
		}
		if err := server.writeJSON(map[string]any{
			"id":     request.ID,
			"result": map[string]any{"turn_id": "turn-1"},
		}); err != nil {
			serverDone <- err
			return
		}
		if err := server.writeJSON(map[string]any{
			"method": "turn/completed",
			"params": map[string]any{"turn_id": "turn-1"},
		}); err != nil {
			serverDone <- err
			return
		}
		serverDone <- nil
	}()

	var result struct {
		TurnID string `json:"turn_id"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Call(ctx, "turn/steer", map[string]string{"text": "continue"}, &result); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.TurnID != "turn-1" {
		t.Fatalf("turn ID = %q, want turn-1", result.TurnID)
	}

	select {
	case notification := <-client.Notifications():
		if notification.Method != "turn/completed" {
			t.Fatalf("notification method = %q, want turn/completed", notification.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for notification")
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server goroutine did not finish")
	}
}

func TestClientCorrelatesConcurrentCallsWithOutOfOrderResponses(t *testing.T) {
	client, server := newPipeClient(t)

	type outcome struct {
		method string
		value  string
		err    error
	}
	outcomes := make(chan outcome, 2)
	var calls sync.WaitGroup
	for _, method := range []string{"first", "second"} {
		calls.Add(1)
		go func(method string) {
			defer calls.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var result string
			err := client.Call(ctx, method, nil, &result)
			outcomes <- outcome{method: method, value: result, err: err}
		}(method)
	}

	requestA := mustReadRequest(t, server)
	requestB := mustReadRequest(t, server)
	mustWriteJSON(t, server, map[string]any{"id": requestB.ID, "result": requestB.Method + "-result"})
	mustWriteJSON(t, server, map[string]any{"id": requestA.ID, "result": requestA.Method + "-result"})
	calls.Wait()
	close(outcomes)

	for outcome := range outcomes {
		if outcome.err != nil {
			t.Fatalf("Call(%q): %v", outcome.method, outcome.err)
		}
		if want := outcome.method + "-result"; outcome.value != want {
			t.Errorf("Call(%q) = %q, want %q", outcome.method, outcome.value, want)
		}
	}
}

func TestClientCancellationRemovesPendingAndIgnoresLateResponse(t *testing.T) {
	client, server := newPipeClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	callDone := make(chan error, 1)
	go func() {
		var result string
		callDone <- client.Call(ctx, "slow", nil, &result)
	}()

	request := mustReadRequest(t, server)
	cancel()
	select {
	case err := <-callDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Call error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled Call did not return")
	}

	writeDone := make(chan error, 1)
	go func() {
		encoder := json.NewEncoder(server.writer)
		if err := encoder.Encode(map[string]any{"id": request.ID, "result": "late"}); err != nil {
			writeDone <- err
			return
		}
		writeDone <- encoder.Encode(map[string]any{"method": "still/alive"})
	}()

	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("write late frames: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("late response blocked the reader")
	}

	select {
	case notification := <-client.Notifications():
		if notification.Method != "still/alive" {
			t.Fatalf("notification method = %q, want still/alive", notification.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("client did not continue after late response")
	}
}

func TestClientCancellationDuringBlockingWriteReturnsAndClosesOwnedTransport(t *testing.T) {
	responseReader, responseWriter := io.Pipe()
	writer := newBlockingWriter()
	client := NewClientWithClose(responseReader, writer, func() error {
		return errors.Join(writer.Close(), responseReader.Close())
	})
	t.Cleanup(func() {
		_ = client.Close()
		_ = responseWriter.Close()
	})

	ctx, cancel := context.WithCancel(context.Background())
	callDone := make(chan error, 1)
	go func() {
		callDone <- client.Call(ctx, "blocked", nil, nil)
	}()
	select {
	case <-writer.started:
	case <-time.After(time.Second):
		t.Fatal("writer did not enter blocking Write")
	}

	cancel()
	select {
	case err := <-callDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Call error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Call did not return after cancellation")
	}
	select {
	case <-writer.closed:
	case <-time.After(time.Second):
		t.Fatal("owned transport was not closed")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close after cancellation: %v", err)
	}
	if calls := writer.closeCalls.Load(); calls != 1 {
		t.Fatalf("owned close calls = %d, want 1", calls)
	}
	client.state.Lock()
	pendingCount := len(client.pending)
	client.state.Unlock()
	if pendingCount != 0 {
		t.Fatalf("pending calls after cancellation = %d, want 0", pendingCount)
	}
}

func TestClientCancellationAfterWriteCompletedDoesNotCloseClient(t *testing.T) {
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()
	var closeCalls atomic.Int32
	client := NewClientWithClose(clientReader, clientWriter, func() error {
		closeCalls.Add(1)
		return errors.Join(clientReader.Close(), clientWriter.Close())
	})
	server := &pipeServer{reader: bufio.NewReader(serverReader), writer: serverWriter}
	t.Cleanup(func() {
		_ = client.Close()
		_ = serverReader.Close()
		_ = serverWriter.Close()
	})

	writeCompleted := make(chan struct{})
	releasePublish := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releasePublish) }) })
	var hookCalls atomic.Int32
	client.afterWriteCompleted = func() {
		if hookCalls.Add(1) == 1 {
			close(writeCompleted)
			<-releasePublish
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() { firstDone <- client.Call(ctx, "first", nil, nil) }()
	_ = mustReadRequest(t, server)
	select {
	case <-writeCompleted:
	case <-time.After(time.Second):
		t.Fatal("writer completion was not observed")
	}
	cancel()
	select {
	case err := <-firstDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("first Call error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first Call did not return after cancellation")
	}
	if calls := closeCalls.Load(); calls != 0 {
		t.Fatalf("owned transport closed %d times after completed write, want 0", calls)
	}
	releaseOnce.Do(func() { close(releasePublish) })

	type outcome struct {
		result string
		err    error
	}
	secondDone := make(chan outcome, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		var result string
		err := client.Call(ctx, "second", nil, &result)
		secondDone <- outcome{result: result, err: err}
	}()
	secondRequest := mustReadRequest(t, server)
	mustWriteJSON(t, server, map[string]any{"id": secondRequest.ID, "result": "ok"})
	select {
	case result := <-secondDone:
		if result.err != nil || result.result != "ok" {
			t.Fatalf("second Call = (%q, %v), want (ok, nil)", result.result, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("second Call did not complete")
	}
}

func TestClientCancellationAfterResponseClaimDoesNotCloseOrFailOtherCalls(t *testing.T) {
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()
	var closeCalls atomic.Int32
	client := NewClientWithClose(clientReader, clientWriter, func() error {
		closeCalls.Add(1)
		return errors.Join(clientReader.Close(), clientWriter.Close())
	})
	server := &pipeServer{reader: bufio.NewReader(serverReader), writer: serverWriter}
	t.Cleanup(func() {
		_ = client.Close()
		_ = serverReader.Close()
		_ = serverWriter.Close()
	})

	responseClaimed := make(chan struct{})
	releaseDispatch := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseDispatch) }) })
	var hookCalls atomic.Int32
	client.afterResponseDispatched = func() {
		if hookCalls.Add(1) == 1 {
			close(responseClaimed)
			<-releaseDispatch
		}
	}
	callWaiting := make(chan struct{})
	releaseCallWait := make(chan struct{})
	var callReleaseOnce sync.Once
	t.Cleanup(func() { callReleaseOnce.Do(func() { close(releaseCallWait) }) })
	var waitHookCalls atomic.Int32
	client.beforeResponseWait = func() {
		if waitHookCalls.Add(1) == 1 {
			close(callWaiting)
			<-releaseCallWait
		}
	}

	type outcome struct {
		result string
		err    error
	}
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstDone := make(chan outcome, 1)
	go func() {
		var result string
		err := client.Call(firstCtx, "first", nil, &result)
		firstDone <- outcome{result: result, err: err}
	}()
	firstRequest := mustReadRequest(t, server)
	select {
	case <-callWaiting:
	case <-time.After(time.Second):
		t.Fatal("first Call did not reach response wait")
	}

	secondDone := make(chan outcome, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		var result string
		err := client.Call(ctx, "second", nil, &result)
		secondDone <- outcome{result: result, err: err}
	}()
	secondRequest := mustReadRequest(t, server)

	mustWriteJSON(t, server, map[string]any{"id": firstRequest.ID, "result": "one"})
	select {
	case <-responseClaimed:
	case <-time.After(time.Second):
		t.Fatal("response dispatch was not observed")
	}
	cancelFirst()
	callReleaseOnce.Do(func() { close(releaseCallWait) })
	select {
	case result := <-firstDone:
		if result.err != nil || result.result != "one" {
			t.Fatalf("first Call = (%q, %v), want (one, nil)", result.result, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("first Call did not consume its claimed response")
	}
	if calls := closeCalls.Load(); calls != 0 {
		t.Fatalf("owned transport closed %d times after response claim, want 0", calls)
	}
	select {
	case result := <-secondDone:
		t.Fatalf("unrelated Call finished early: (%q, %v)", result.result, result.err)
	default:
	}

	releaseOnce.Do(func() { close(releaseDispatch) })
	mustWriteJSON(t, server, map[string]any{"id": secondRequest.ID, "result": "two"})
	select {
	case result := <-secondDone:
		if result.err != nil || result.result != "two" {
			t.Fatalf("second Call = (%q, %v), want (two, nil)", result.result, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("unrelated Call did not complete")
	}
}

func TestClientResultAfterDoneRespectsPendingOwnership(t *testing.T) {
	terminalErr := errors.New("terminal")

	t.Run("claimed response", func(t *testing.T) {
		response := make(chan callResult, 1)
		response <- callResult{result: json.RawMessage(`"claimed-response"`)}
		done := make(chan struct{})
		close(done)
		client := &Client{
			pending:     make(map[int64]chan callResult),
			terminalErr: terminalErr,
			done:        done,
		}

		var result string
		if err := client.resultAfterDone(1, response, &result); err != nil {
			t.Fatalf("resultAfterDone: %v", err)
		}
		if result != "claimed-response" {
			t.Fatalf("result = %q, want claimed-response", result)
		}
	})

	t.Run("pending remains", func(t *testing.T) {
		response := make(chan callResult, 1)
		done := make(chan struct{})
		close(done)
		client := &Client{
			pending:     map[int64]chan callResult{1: response},
			terminalErr: terminalErr,
			done:        done,
		}

		err := client.resultAfterDone(1, response, nil)
		if !errors.Is(err, terminalErr) {
			t.Fatalf("resultAfterDone error = %v, want terminal error", err)
		}
		if len(client.pending) != 0 {
			t.Fatalf("pending calls = %d, want 0", len(client.pending))
		}
	})
}

func TestClientTerminatesOnInvalidWriterCount(t *testing.T) {
	tests := []struct {
		name  string
		count func(int) int
	}{
		{name: "negative", count: func(int) int { return -1 }},
		{name: "too large", count: func(length int) int { return length + 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			responseReader, responseWriter := io.Pipe()
			client := NewClientWithClose(responseReader, invalidCountWriter{count: test.count}, responseReader.Close)
			t.Cleanup(func() {
				_ = client.Close()
				_ = responseWriter.Close()
			})

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := client.Call(ctx, "invalid-write", nil, nil)
			if !errors.Is(err, ErrInvalidWriteCount) {
				t.Fatalf("Call error = %v, want ErrInvalidWriteCount", err)
			}
		})
	}
}

func TestClientRejectsOversizedOutboundFrameWithoutClosingClient(t *testing.T) {
	responseReader, responseWriter := io.Pipe()
	writer := newRecordingWriter()
	client := NewClient(responseReader, writer)
	t.Cleanup(func() {
		_ = client.Close()
		_ = responseWriter.Close()
	})

	err := client.Call(context.Background(), "oversized", strings.Repeat("x", maxFrameSize), nil)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversized Call error = %v, want ErrFrameTooLarge", err)
	}
	if got := len(writer.bytes()); got != 0 {
		t.Fatalf("writer received %d bytes for oversized frame, want 0", got)
	}
	client.state.Lock()
	pendingCount := len(client.pending)
	client.state.Unlock()
	if pendingCount != 0 {
		t.Fatalf("pending calls after oversized frame = %d, want 0", pendingCount)
	}

	type healthyOutcome struct {
		result string
		err    error
	}
	callDone := make(chan healthyOutcome, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		var result string
		err := client.Call(ctx, "healthy", nil, &result)
		callDone <- healthyOutcome{result: result, err: err}
	}()

	select {
	case <-writer.written:
	case <-time.After(time.Second):
		t.Fatal("healthy Call did not write a request")
	}
	var request Request
	if err := json.Unmarshal(bytes.TrimSpace(writer.bytes()), &request); err != nil {
		t.Fatalf("decode healthy request: %v", err)
	}
	if err := json.NewEncoder(responseWriter).Encode(map[string]any{"id": request.ID, "result": "ok"}); err != nil {
		t.Fatalf("write healthy response: %v", err)
	}
	var outcome healthyOutcome
	select {
	case outcome = <-callDone:
	case <-time.After(time.Second):
		t.Fatal("healthy Call did not finish")
	}
	if outcome.err != nil {
		t.Fatalf("healthy Call after oversized frame: %v", outcome.err)
	}
	if outcome.result != "ok" {
		t.Fatalf("healthy Call result = %q, want ok", outcome.result)
	}
}

func TestClientOutboundPayloadSizeBoundary(t *testing.T) {
	t.Run("exact limit", func(t *testing.T) {
		responseReader, responseWriter := io.Pipe()
		writer := newRecordingWriter()
		client := NewClientWithClose(responseReader, writer, responseReader.Close)
		t.Cleanup(func() {
			_ = client.Close()
			_ = responseWriter.Close()
		})

		params := requestParamsForPayloadSize(t, "boundary", 1, maxFrameSize)
		type outcome struct {
			result string
			err    error
		}
		callDone := make(chan outcome, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var result string
			err := client.Call(ctx, "boundary", params, &result)
			callDone <- outcome{result: result, err: err}
		}()
		select {
		case <-writer.written:
		case <-time.After(time.Second):
			t.Fatal("exact-limit request was not written")
		}

		frame := writer.bytes()
		if len(frame) != maxFrameSize+1 || frame[len(frame)-1] != '\n' {
			t.Fatalf("encoded JSONL frame length = %d, want %d including newline", len(frame), maxFrameSize+1)
		}
		var request Request
		if err := json.Unmarshal(frame[:len(frame)-1], &request); err != nil {
			t.Fatalf("decode exact-limit request: %v", err)
		}
		if err := json.NewEncoder(responseWriter).Encode(map[string]any{"id": request.ID, "result": "ok"}); err != nil {
			t.Fatalf("write exact-limit response: %v", err)
		}
		var result outcome
		select {
		case result = <-callDone:
		case <-time.After(time.Second):
			t.Fatal("exact-limit Call did not finish")
		}
		if result.err != nil || result.result != "ok" {
			t.Fatalf("exact-limit Call = (%q, %v), want (ok, nil)", result.result, result.err)
		}
	})

	t.Run("limit plus one", func(t *testing.T) {
		responseReader, responseWriter := io.Pipe()
		writer := newRecordingWriter()
		client := NewClientWithClose(responseReader, writer, responseReader.Close)
		t.Cleanup(func() {
			_ = client.Close()
			_ = responseWriter.Close()
		})

		params := requestParamsForPayloadSize(t, "boundary", 1, maxFrameSize+1)
		err := client.Call(context.Background(), "boundary", params, nil)
		if !errors.Is(err, ErrFrameTooLarge) {
			t.Fatalf("limit-plus-one Call error = %v, want ErrFrameTooLarge", err)
		}
		if got := len(writer.bytes()); got != 0 {
			t.Fatalf("writer received %d bytes for limit-plus-one payload, want 0", got)
		}
	})
}

func TestClientInboundPayloadSizeBoundary(t *testing.T) {
	t.Run("exact limit", func(t *testing.T) {
		payload := notificationPayloadForSize(t, maxFrameSize)
		client := NewClient(strings.NewReader(string(payload)+"\n"), io.Discard)
		t.Cleanup(func() { _ = client.Close() })
		select {
		case notification, ok := <-client.Notifications():
			if !ok || notification.Method != "boundary" {
				t.Fatalf("exact-limit notification = (%q, %v), want (boundary, true)", notification.Method, ok)
			}
		case <-time.After(time.Second):
			t.Fatal("exact-limit inbound payload was not accepted")
		}
	})

	t.Run("limit plus one", func(t *testing.T) {
		payload := notificationPayloadForSize(t, maxFrameSize+1)
		client := NewClient(strings.NewReader(string(payload)+"\n"), io.Discard)
		t.Cleanup(func() { _ = client.Close() })
		select {
		case _, ok := <-client.Notifications():
			if ok {
				t.Fatal("limit-plus-one inbound payload produced a notification")
			}
		case <-time.After(time.Second):
			t.Fatal("limit-plus-one inbound payload did not terminate client")
		}
		if err := client.Call(context.Background(), "after-oversize", nil, nil); !errors.Is(err, ErrFrameTooLarge) {
			t.Fatalf("Call after limit-plus-one inbound payload = %v, want ErrFrameTooLarge", err)
		}
	})
}

func TestClientTerminatesOnNotificationOverflow(t *testing.T) {
	client, server := newPipeClient(t)
	callDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		callDone <- client.Call(ctx, "pending", nil, nil)
	}()
	_ = mustReadRequest(t, server)

	for index := 0; index <= notificationChannelBuffer; index++ {
		err := writeJSONWithTimeout(server, map[string]any{"method": "event", "params": map[string]int{"index": index}})
		if err != nil && index < notificationChannelBuffer {
			t.Fatalf("write notification %d: %v", index, err)
		}
	}
	select {
	case err := <-callDone:
		if !errors.Is(err, ErrNotificationOverflow) {
			t.Fatalf("pending Call error = %v, want ErrNotificationOverflow", err)
		}
	case <-time.After(time.Second):
		t.Fatal("notification overflow did not fail pending Call")
	}

	received := 0
	for {
		select {
		case _, ok := <-client.Notifications():
			if !ok {
				if received != notificationChannelBuffer {
					t.Fatalf("buffered notifications = %d, want %d", received, notificationChannelBuffer)
				}
				return
			}
			received++
		case <-time.After(time.Second):
			t.Fatal("notifications channel did not close after overflow")
		}
	}
}

func TestClientReaderFailuresFailPendingAndCloseNotifications(t *testing.T) {
	tests := []struct {
		name    string
		trigger func(*testing.T, *pipeServer)
	}{
		{
			name: "malformed JSON",
			trigger: func(t *testing.T, server *pipeServer) {
				if _, err := io.WriteString(server.writer, "{not-json}\n"); err != nil {
					t.Fatalf("write malformed frame: %v", err)
				}
			},
		},
		{
			name: "oversized frame",
			trigger: func(t *testing.T, server *pipeServer) {
				_, _ = io.WriteString(server.writer, strings.Repeat("x", maxFrameSize+1)+"\n")
			},
		},
		{
			name: "EOF",
			trigger: func(t *testing.T, server *pipeServer) {
				if err := server.writer.Close(); err != nil {
					t.Fatalf("close server writer: %v", err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, server := newPipeClient(t)
			callDone := make(chan error, 1)
			go func() {
				var result string
				callDone <- client.Call(context.Background(), "pending", nil, &result)
			}()
			_ = mustReadRequest(t, server)
			test.trigger(t, server)

			select {
			case err := <-callDone:
				if err == nil {
					t.Fatal("pending Call unexpectedly succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("pending Call was not failed")
			}

			select {
			case _, ok := <-client.Notifications():
				if ok {
					t.Fatal("notifications channel remained open")
				}
			case <-time.After(time.Second):
				t.Fatal("notifications channel was not closed")
			}
		})
	}
}

func TestClientReturnsRPCError(t *testing.T) {
	client, server := newPipeClient(t)
	serverDone := make(chan error, 1)
	go func() {
		request, err := server.readRequest()
		if err != nil {
			serverDone <- err
			return
		}
		serverDone <- server.writeJSON(map[string]any{
			"id": request.ID,
			"error": map[string]any{
				"code":    -32602,
				"message": "invalid params",
				"data":    map[string]any{"field": "text"},
			},
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := client.Call(ctx, "bad", nil, nil)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("Call error = %T %v, want *RPCError", err, err)
	}
	if rpcErr.Code != -32602 || rpcErr.Message != "invalid params" {
		t.Fatalf("RPC error = (%d, %q), want (-32602, invalid params)", rpcErr.Code, rpcErr.Message)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server goroutine did not finish")
	}
}

func TestClientCloseIsIdempotentAndRejectsNewCalls(t *testing.T) {
	client := NewClient(strings.NewReader(""), io.Discard)
	if err := client.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := client.Call(context.Background(), "after-close", nil, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("Call after Close = %v, want ErrClosed", err)
	}
}

func TestClientNotifyWritesNotificationWithoutRequestID(t *testing.T) {
	client, server := newPipeClient(t)
	done := make(chan error, 1)
	go func() {
		line, err := server.reader.ReadBytes('\n')
		if err != nil {
			done <- err
			return
		}
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(bytes.TrimSpace(line), &envelope); err != nil {
			done <- err
			return
		}
		if _, ok := envelope["id"]; ok {
			done <- errors.New("notification unexpectedly contains an id")
			return
		}
		var method string
		if err := json.Unmarshal(envelope["method"], &method); err != nil {
			done <- err
			return
		}
		if method != "initialized" {
			done <- fmt.Errorf("method = %q, want initialized", method)
			return
		}
		done <- nil
	}()

	if err := client.Notify(context.Background(), "initialized", map[string]any{}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out reading notification")
	}
}

func TestClientNotifyRejectsOversizedFrameWithoutWriting(t *testing.T) {
	responseReader, responseWriter := io.Pipe()
	writer := newRecordingWriter()
	client := NewClient(responseReader, writer)
	t.Cleanup(func() {
		_ = client.Close()
		_ = responseWriter.Close()
	})

	err := client.Notify(context.Background(), "oversized", strings.Repeat("x", maxFrameSize))
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("Notify error = %v, want ErrFrameTooLarge", err)
	}
	if got := len(writer.bytes()); got != 0 {
		t.Fatalf("writer received %d bytes for oversized notification, want 0", got)
	}
}
