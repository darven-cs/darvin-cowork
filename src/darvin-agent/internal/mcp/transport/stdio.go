package transport

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"go.uber.org/zap"
)

// StdioTransport speaks LSP-style Content-Length framed JSON-RPC over the
// child process stdio. The MCP reference servers (filesystem, git, etc.)
// all use this layout, so the same struct also covers third-party servers
// that wrap `@modelcontextprotocol/server-*`.
type StdioTransport struct {
	Command string
	Args    []string
	Env     map[string]string
	Logger  *zap.Logger

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
	alive  atomic.Bool
	// waitCh is closed once the child has been reaped by cmd.Wait. Only the
	// Connect watcher goroutine calls Wait; Close waits on this channel
	// instead of calling Wait a second time (os/exec.Cmd.Wait is not
	// safe to invoke concurrently).
	waitCh chan struct{}
	mu     sync.Mutex // guards Connect/Close; Send/Recv are serialized by the Client.
}

const stdioCloseGrace = 5 * time.Second

func (s *StdioTransport) Connect(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.alive.Load() {
		return nil
	}

	// 子进程生命周期由 transport 自己通过 Close 管理(SIGTERM→SIGKILL),
	// 不绑定 caller 的 ctx —— connectServer 的 ctx 在连接建好后就会被 cancel,
	// 绑上会导致刚连上的 MCP server 立刻被杀。
	cmd := exec.Command(s.Command, s.Args...) //nolint:gosec // spawn point: config-driven MCP server command from user.
	cmd.Env = append([]string{}, os.Environ()...)
	for k, v := range s.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

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

	// Drain stderr into zap so server logs surface in the agent's main log.
	exitCh := make(chan struct{})
	go func() {
		defer close(exitCh)
		if s.Logger == nil {
			_, _ = io.Copy(io.Discard, stderr)
			return
		}
		scanner := bufio.NewScanner(stderr)
		// Increase buffer in case a server emits very long lines.
		scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for scanner.Scan() {
			s.Logger.Debug("mcp-stdio-stderr", zap.String("line", scanner.Text()))
		}
	}()

	// Watch for the child exiting on its own: once Wait returns, the
	// pipes are dead and any in-flight Send/Recv will fail.
	s.waitCh = make(chan struct{})
	go func() {
		_ = cmd.Wait()
		s.alive.Store(false)
		close(s.waitCh)
		<-exitCh
	}()

	return nil
}

func (s *StdioTransport) Send(ctx context.Context, body []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.alive.Load() {
		return ErrTransportClosed
	}

	header := "Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n"
	full := make([]byte, 0, len(header)+len(body))
	full = append(full, header...)
	full = append(full, body...)

	if _, err := s.stdin.Write(full); err != nil {
		s.alive.Store(false)
		return fmt.Errorf("mcp stdio: write: %w", err)
	}
	return nil
}

func (s *StdioTransport) Recv(ctx context.Context) (Frame, error) {
	if err := ctx.Err(); err != nil {
		return Frame{}, err
	}
	if !s.alive.Load() {
		return Frame{}, ErrTransportClosed
	}

	reader := bufio.NewReader(s.stdout)

	contentLength := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			s.alive.Store(false)
			if errors.Is(err, io.EOF) {
				return Frame{}, io.EOF
			}
			return Frame{}, fmt.Errorf("mcp stdio: read header: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		const prefix = "Content-Length: "
		if strings.HasPrefix(line, prefix) {
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, prefix)))
			if err != nil {
				return Frame{}, fmt.Errorf("mcp stdio: bad Content-Length %q: %w", line, err)
			}
			contentLength = n
		}
	}

	if contentLength <= 0 {
		return Frame{}, errors.New("mcp stdio: missing Content-Length header")
	}

	body := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, body); err != nil {
		s.alive.Store(false)
		return Frame{}, fmt.Errorf("mcp stdio: read body: %w", err)
	}
	return Frame{Body: body}, nil
}

func (s *StdioTransport) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.alive.CompareAndSwap(true, false) {
		return nil
	}

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

	// Try SIGTERM first; fall back to SIGKILL after stdioCloseGrace. The
	// Connect watcher goroutine owns cmd.Wait; waiting on waitCh reaps the
	// child without a second Wait call.
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

func (s *StdioTransport) Alive() bool {
	return s.alive.Load()
}
