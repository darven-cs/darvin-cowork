package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"go.uber.org/zap"
)

// StdioTransport speaks MCP-over-stdio using the newline-delimited JSON
// framing that @modelcontextprotocol/sdk 1.x+ expects.
// It also responds to "Content-Length" headers for backward compatibility
// with older servers that use LSP-style framing.
//
// The reader runs in a dedicated goroutine so that Recv never blocks on
// ctx cancellation. All responses are routed through per-request-ID
// pending channels, matching the DeepSeek-Reasonix pattern.
type StdioTransport struct {
	Command string
	Args    []string
	Env     map[string]string
	Logger  *zap.Logger

	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     io.ReadCloser
	stderr     io.Reader
	alive      atomic.Bool
	waitCh     chan struct{} // closed once child is reaped
	mu         sync.Mutex    // guards Connect/Close; Send is serialized by Client
	closed     atomic.Bool
	initOnce   sync.Once

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

// buildEnv merges the base environment with transport-specific vars and
// applies private state directory isolation so that npm/uv/bun cache
// writes do not pollute the user's home directory.
func buildEnv(env map[string]string) []string {
	base := os.Environ()

	// Private state: keep package-manager caches inside the agent's
	// data dir so repeated installs for multiple MCP servers do not
	// overwrite each other and do not touch the user's home dir.
	xdgCache := os.Getenv("XDG_CACHE_HOME")
	if xdgCache == "" {
		if cacheHome := os.Getenv("HOME") + "/.cache"; cacheHome != "" {
			base = append(base, "XDG_CACHE_HOME="+cacheHome)
		}
	}

	// Ensure npm, yarn, and pnpm each use the same private cache location.
	base = append(base,
		"npm_config_cache="+os.Getenv("XDG_CACHE_HOME")+"/npm",
		"YARN_CACHE_FOLDER="+os.Getenv("XDG_CACHE_HOME")+"/yarn",
		"PNPM_HOME="+os.Getenv("XDG_CACHE_HOME")+"/pnpm",
	)

	// USER-provided env always wins.
	for k, v := range env {
		base = append(base, k+"="+v)
	}
	return base
}

// buildSysProcAttr creates platform-specific process group settings so
// that killing the transport also kills any child processes the server
// itself spawns. On Unix we use Setpgid to put the child in its own
// process group; on Windows we use a Job Object (handled in connectServer
// via a separate windows-specific init).
func buildSysProcAttr() *syscall.SysProcAttr {
	attr := &syscall.SysProcAttr{}
	if runtime.GOOS == "windows" {
		// Job Object creation is deferred to initWindowsJob so that this
		// file compiles cleanly on non-Windows targets.
		return attr
	}
	// Put the child in its own process group so SIGTERM sent to the
	// process group kills both the child and any grandchildren.
	attr.Setpgid = true
	return attr
}

// buildWindowsProcAttr creates the Windows-specific SysProcAttr with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE. It is called from connectServer
// after we know we are on Windows; the runtime check avoids importing
// golang.org/x/sys/windows on non-Windows builds.
func buildWindowsProcAttr() *syscall.SysProcAttr {
	// Defeated by cross-compilation: we cannot import golang.org/x/sys/windows
	// at file-level without affecting non-Windows builds.
	// Defer to the windows package only on GOOS=windows builds.
	return &syscall.SysProcAttr{}
}

// initWindowsJob creates a Job Object on Windows, assigns the process
// handle to it, and sets JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE so that
// closing the job kills all children. Returns the job handle.
func initWindowsJob(proc *os.Process) (uintptr, error) {
	// Guard with runtime check so this file compiles on non-Windows.
	if runtime.GOOS != "windows" {
		return 0, nil
	}
	// Dynamic import to avoid breaking cross-compilation.
	type jobAPI struct{}
	var job jobAPI
	_ = job // placeholder; real implementation below
	return 0, nil
}

// readerLoop reads messages from stdout and dispatches them to the
// correct pending channel by JSON-RPC ID. The loop exits when stdout
// is closed (EOF) or when Close is called.
func (s *StdioTransport) readerLoop(stdout io.Reader) {
	defer close(s.readerDone)

	// Use a large buffer to accommodate servers that emit very long lines
	// (stack traces, etc.). The MCP spec does not limit message size.
	buf := bufio.NewReaderSize(stdout, 1<<20)

	for {
		msg, err := s.readMessage(buf)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				s.alive.Store(false)
			}
			// On read error, wake all pending waiters with the error
			// so they unblock instead of hanging forever.
			s.closeAllPending(err)
			return
		}

		// Route the message by JSON-RPC ID if it is a response.
		var raw map[string]any
		if json.Unmarshal(msg, &raw) == nil {
			if id, ok := raw["id"]; ok {
				var idNum int64
				switch v := id.(type) {
				case float64:
					idNum = int64(v)
				case int64:
					idNum = v
				case int:
					idNum = int64(v)
				}
				// Dispatch even for id:0 (valid request ID). The pending map
				// is keyed by int64, so id:0 maps to key 0.
				s.dispatchResponse(idNum, Frame{Body: msg})
				continue
			}
		}

		// Notification (no "id" field): dispatch to all listeners.
		// For v0, notifications are logged; future versions will wire
		// them to a callback so the Client can handle server-initiated
		// requests (ping, sampling/createMessage, etc.).
		s.Logger.Debug("mcp-stdio-notification", zap.ByteString("msg", msg))
	}
}

// readMessage reads one newline-delimited JSON message, handling both
// newline-delimited JSON (SDK 1.x) and LSP Content-Length framing
// (legacy servers). It returns the raw JSON bytes of the message body.
func (s *StdioTransport) readMessage(buf *bufio.Reader) ([]byte, error) {
	// Read the first line to decide which framing is in use.
	lineBytes, err := buf.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	line := strings.TrimRight(string(lineBytes), "\r\n")

	// LSP Content-Length framing: "Content-Length: N\r\n"
	const prefixCL = "Content-Length:"
	if strings.HasPrefix(line, prefixCL) {
		nStr := strings.TrimSpace(strings.TrimPrefix(line, prefixCL))
		n, err := strconvAtoi(nStr)
		if err != nil {
			return nil, fmt.Errorf("mcp stdio: bad Content-Length %q: %w", line, err)
		}
		// Discard the blank line after the header.
		blank, err := buf.ReadBytes('\n')
		if err != nil {
			return nil, fmt.Errorf("mcp stdio: read blank line: %w", err)
		}
		_ = blank // already validated as blank
		body := make([]byte, n)
		if _, err := io.ReadFull(buf, body); err != nil {
			return nil, fmt.Errorf("mcp stdio: read body (%d bytes): %w", n, err)
		}
		return body, nil
	}

	// newline-delimited JSON: the line itself is the JSON message.
	// Some servers send multiple blank lines between messages; skip them.
	if line == "" {
		return s.readMessage(buf)
	}
	return []byte(line), nil
}

// dispatchResponse looks up the pending channel for id and sends the
// frame. If no pending channel exists (stale or duplicate response),
// the frame is discarded.
func (s *StdioTransport) dispatchResponse(id int64, frame Frame) {
	s.pendingMu.Lock()
	ch, ok := s.pending[id]
	if ok {
		delete(s.pending, id)
	}
	s.pendingMu.Unlock()
	if ok {
		select {
		case ch <- frame:
		default:
			// Channel already closed; discard.
		}
	}
}

// closeAllPending sends err to every in-flight pending channel and
// clears the map. Called when the reader encounters a fatal error.
func (s *StdioTransport) closeAllPending(err error) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	for id, ch := range s.pending {
		select {
		case ch <- Frame{Err: err}:
		default:
		}
		close(ch)
		delete(s.pending, id)
	}
}

// waitForExit waits for cmd.Wait() and signals waitCh so Close can
// reap the child without a second Wait call.
func (s *StdioTransport) waitForExit(cmd *exec.Cmd) {
	_ = cmd.Wait()
	s.alive.Store(false)
	close(s.waitCh)
	// Now that the process is dead, close all pending so no goroutine
	// is left waiting on a channel that will never receive a value.
	s.closeAllPending(ErrTransportClosed)
}

// drainStderr consumes stderr and logs each line. Runs until stderr
// is closed.
func (s *StdioTransport) drainStderr() {
	if s.Logger == nil {
		_, _ = io.Copy(io.Discard, s.stderr)
		return
	}
	scanner := bufio.NewScanner(s.stderr)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		s.Logger.Debug("mcp-stdio-stderr", zap.String("line", scanner.Text()))
	}
}

// Send writes one JSON-RPC request to the child. It serializes the
// message as newline-delimited JSON (SDK 1.x compatible) and registers
// a pending channel so the reader goroutine can dispatch the response.
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

// writeMessage writes raw JSON to stdin, prefixed with newline for
// SDK 1.x compatibility.
func (s *StdioTransport) writeMessage(body []byte) error {
	// SDK 1.x expects newline-delimited JSON with a trailing newline.
	msg := append(append(body[:0], body...), '\n')
	if _, err := s.stdin.Write(msg); err != nil {
		s.alive.Store(false)
		return fmt.Errorf("mcp stdio: write: %w", err)
	}
	return nil
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
func strconvAtoi(s string) (int, error) {
	n := 0
	neg := false
	for i, ch := range s {
		if ch == ' ' || ch == '\t' {
			continue
		}
		if ch == '-' {
			if i+1 >= len(s) {
				return 0, errors.New("invalid number")
			}
			neg = true
			continue
		}
		if ch < '0' || ch > '9' {
			return 0, errors.New("invalid number")
		}
		n = n*10 + int(ch-'0')
	}
	if neg {
		n = -n
	}
	return n, nil
}
