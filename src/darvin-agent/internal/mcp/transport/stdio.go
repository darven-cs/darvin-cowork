// Speaks MCP JSON-RPC over a child process's stdio.

package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"go.uber.org/zap"
)

// StdioTransport speaks MCP-over-stdio using newline-delimited JSON
// (SDK 1.x), and responds to Content-Length headers for legacy LSP-style
// servers. The reader runs in a dedicated goroutine; responses are
// routed through per-request-ID pending channels so concurrent calls do
// not block each other.
type StdioTransport struct {
	Command string
	Args    []string
	Env     map[string]string
	Logger  *zap.Logger

	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   io.ReadCloser
	stderr   io.Reader
	alive    atomic.Bool
	waitCh   chan struct{} // closed once child is reaped
	mu       sync.Mutex    // guards Connect/Close; Send is serialized by Client
	closed   atomic.Bool
	initOnce sync.Once

	// Reader goroutine state.
	readerDone chan struct{}
	pendingMu  sync.Mutex
	pending    map[int64]chan Frame // keyed by JSON-RPC request ID

	// lastFrame stores the most recently received response so that Recv
	// can return it after Send has already waited for the response via the
	// pending channel. It is only accessed under the Client's mutex.
	lastFrame Frame
}

// stdioCloseGrace is the time we wait for a well-behaved server to exit
// after we close stdin before escalating to SIGKILL.
const stdioCloseGrace = 750 * time.Millisecond

// ErrUnknownResponse is returned when a JSON-RPC response ID does not match
// any in-flight request. This guards against corrupted or unexpected messages.
var ErrUnknownResponse = errors.New("mcp stdio: response id has no matching request")

func (s *StdioTransport) Connect(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.initOnce.Do(func() {
		s.pending = make(map[int64]chan Frame)
		s.readerDone = make(chan struct{})
		s.waitCh = make(chan struct{}, 1) // buffered so close() always succeeds
	})

	if s.alive.Load() {
		return nil
	}

	cmd := exec.Command(s.Command, s.Args...) //nolint:gosec // config-driven spawn
	cmd.Env = buildEnv(s.Env)
	cmd.SysProcAttr = buildSysProcAttr()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("mcp stdio: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("mcp stdio: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return fmt.Errorf("mcp stdio: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return fmt.Errorf("mcp stdio: spawn %q: %w", s.Command, err)
	}

	s.cmd = cmd
	s.stdin = stdin
	s.stdout = stdout
	s.stderr = stderr
	s.alive.Store(true)

	// Drain stderr into the logger so server logs surface in the agent log.
	go s.drainStderr()

	// Start the reader goroutine before returning so that the first
	// response can be collected even if Connect returns before the
	// server has written anything.
	go s.readerLoop(stdout)

	// Watch for the child exiting on its own; once Wait returns the
	// pipes are dead and the reader will eventually hit EOF.
	go s.waitForExit(cmd)

	return nil
}

// Send writes body to the child's stdin and waits for the matching
// response to be routed back by the reader goroutine.
func (s *StdioTransport) Send(ctx context.Context, body []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.alive.Load() {
		return ErrTransportClosed
	}

	// Extract the JSON-RPC ID so Recv can wait on the right channel.
	// Extract outside the lock to avoid holding the mutex during I/O.
	// We must distinguish "no id field" (notification) from "id: 0" (real request).
	var rawBody map[string]any
	if err := json.Unmarshal(body, &rawBody); err != nil {
		// Malformed JSON — not a valid request; send it as-is.
		return s.writeMessage(body)
	}
	rawID, hasID := rawBody["id"]
	if !hasID {
		// Notification (no id field) — cannot be waited on, just send.
		return s.writeMessage(body)
	}
	// id field exists; extract the numeric ID. Ints and floats are both
	// valid JSON numbers. Servers always return int64.
	var id int64
	switch v := rawID.(type) {
	case float64:
		id = int64(v)
	case int64:
		id = v
	case int:
		id = int64(v)
	}
	if id == 0 {
		// id:0 is a valid request ID, not a notification.
		// Fall through to register a pending channel.
	}

	// Register pending channel before sending so that a fast server
	// response cannot arrive before we are ready to receive it.
	ch := make(chan Frame, 1)
	s.pendingMu.Lock()
	s.pending[id] = ch
	s.pendingMu.Unlock()

	if err := s.writeMessage(body); err != nil {
		// Send failed; clean up the pending entry.
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
		return err
	}

	// Wait for the response with ctx cancellation support.
	select {
	case frame, ok := <-ch:
		if !ok {
			return ErrTransportClosed
		}
		if frame.Err != nil {
			return frame.Err
		}
		// Store so Recv (called by Client.Call under the same mutex)
		// can return the frame.
		s.lastFrame = frame
		return nil
	case <-ctx.Done():
		// Caller cancelled; clean up the pending entry so the
		// reader does not send to a stale channel.
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
		return ctx.Err()
	}
}

// Recv returns the frame that Send already collected via the pending
// channel. Called by Client.Call after Send has returned; the Client
// holds the mutex throughout the Send+Recv pair so lastFrame is safe
// to access without locking here.
func (s *StdioTransport) Recv(ctx context.Context) (Frame, error) {
	if err := ctx.Err(); err != nil {
		return Frame{}, err
	}
	if !s.alive.Load() {
		return Frame{}, ErrTransportClosed
	}
	frame := s.lastFrame
	s.lastFrame = Frame{} // clear so the next Call starts fresh
	return frame, nil
}

// Close gracefully terminates the child process. It closes stdin,
// waits stdioCloseGrace for the process to exit, then escalates to
// SIGKILL. All pending requests are woken with an error.
func (s *StdioTransport) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.alive.Store(false)

	// Close stdin first so the server sees EOF and can flush its output.
	if s.stdin != nil {
		_ = s.stdin.Close()
	}

	if s.stdout != nil {
		_ = s.stdout.Close()
	}

	cmd := s.cmd
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	// Graceful termination: SIGTERM → wait → SIGKILL.
	// On Unix, we use Process.Signal which sends to the process group
	// if SysProcAttr.Setpgid is set.
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-s.waitCh:
		return nil
	case <-time.After(stdioCloseGrace):
		_ = cmd.Process.Kill()
		<-s.waitCh
		return nil
	}
}

// Alive reports whether the transport is still alive.
func (s *StdioTransport) Alive() bool {
	return s.alive.Load()
}

// strconvAtoi is a wrapper that avoids importing strconv in the same
// file that uses bufio.Reader (no conflict, but keeping it explicit).
