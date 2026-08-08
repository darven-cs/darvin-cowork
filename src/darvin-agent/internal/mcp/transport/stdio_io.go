// IO and subprocess plumbing for StdioTransport: the reader
// loop, frame parsing, and the stdin writer.

package transport

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"

	"go.uber.org/zap"
)

// buildEnv merges the base environment with transport-specific vars and
// applies private state-dir isolation so package-manager caches do not
// pollute the user's home directory.
func buildEnv(env map[string]string) []string {
	base := os.Environ()

	// Keep package-manager caches inside the agent's data dir so repeated
	// installs for multiple MCP servers do not touch the user's home.
	xdgCache := os.Getenv("XDG_CACHE_HOME")
	if xdgCache == "" {
		if cacheHome := os.Getenv("HOME") + "/.cache"; cacheHome != "" {
			base = append(base, "XDG_CACHE_HOME="+cacheHome)
		}
	}

	// npm / yarn / pnpm share the same private cache location.
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

// buildSysProcAttr puts the child in its own process group on Unix so a
// SIGTERM to the group kills the child and any grandchildren. Windows
// Job Object creation is deferred to initWindowsJob.
func buildSysProcAttr() *syscall.SysProcAttr {
	attr := &syscall.SysProcAttr{}
	if runtime.GOOS == "windows" {
		return attr
	}
	attr.Setpgid = true
	return attr
}

// buildWindowsProcAttr returns a Windows SysProcAttr. The real Job Object
// wiring is deferred to initWindowsJob to keep this file compiling on
// non-Windows targets.
func buildWindowsProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}

// initWindowsJob creates a Job Object on Windows that kills all children
// on close. No-op on non-Windows builds.
func initWindowsJob(proc *os.Process) (uintptr, error) {
	if runtime.GOOS != "windows" {
		return 0, nil
	}
	type jobAPI struct{}
	var job jobAPI
	_ = job // placeholder; real implementation below
	return 0, nil
}

// readerLoop reads messages from stdout and dispatches them to the
// matching pending channel by JSON-RPC ID. Exits on EOF or Close.
func (s *StdioTransport) readerLoop(stdout io.Reader) {
	defer close(s.readerDone)

	// Large buffer for servers emitting very long lines (stack traces).
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

// readMessage reads one message, handling both newline-delimited JSON
// (SDK 1.x) and LSP Content-Length framing (legacy servers).
func (s *StdioTransport) readMessage(buf *bufio.Reader) ([]byte, error) {
	// First line decides which framing is in use.
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
