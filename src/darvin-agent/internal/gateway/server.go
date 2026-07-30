package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// upgrader is shared across connections. CheckOrigin always returns
// true: S3 binds localhost only, and v0 has no auth layer; tightening
// this is an S5+ concern (alongside Bearer-token auth).
var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// Server owns the bound listener, the embedded *http.Server, and the
// per-connection state. Start binds the port and Shutdown drains it.
type Server struct {
	addr    string
	handler *Handler
	log     *zap.Logger

	mu       sync.Mutex
	listener net.Listener
	httpSrv  *http.Server
	port     int
	started  bool
}

// NewServer wires the *Handler (which carries the SessionManager /
// EventLedger / acp.Loop / acp.SteerControl dependencies) and the
// server-local zap logger. addr is "localhost:0" so the OS picks a free
// port; the actual port is reported via the stdout contract below.
func NewServer(h *Handler, log *zap.Logger) *Server {
	return &Server{
		addr:    "localhost:0",
		handler: h,
		log:     log,
	}
}

// Start binds the listener, prints the single-line port announcement on
// stdout, and launches the http.Server in a goroutine. The call is
// non-blocking: control returns once the port is bound.
//
// stdout contract: a single line of the form `<port>NNNNN</port>\n`,
// followed by os.Stdout.Sync(). The Electron RuntimeMgr (S5) reads
// this exact line — any extra stdout noise will break the parser, so
// every other log line in the package targets stderr.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return errors.New("gateway: server already started")
	}

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("gateway: listen: %w", err)
	}
	s.listener = ln
	s.started = true

	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("gateway: listener address is %T, not *net.TCPAddr", ln.Addr())
	}
	s.port = tcpAddr.Port
	s.mu.Unlock()

	// stdout: single line, then sync so the parent process sees it
	// before any subprocess that may read it.
	if _, err := fmt.Fprintf(os.Stdout, "<port>%d</port>\n", s.port); err != nil {
		return fmt.Errorf("gateway: write port line: %w", err)
	}
	_ = os.Stdout.Sync()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS)
	s.mu.Lock()
	s.httpSrv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.mu.Unlock()

	go func() {
		if err := s.httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Error("gateway http serve", zap.Error(err))
		}
	}()

	s.log.Info("gateway listening", zap.Int("port", s.port))
	return nil
}

// Port returns the OS-assigned port. Only valid after Start returns
// nil. The Electron RuntimeMgr calls this after the stdout parse.
func (s *Server) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.port
}

// Shutdown stops accepting new connections and waits up to ctx's
// deadline for in-flight requests to finish. Existing WebSocket loops
// are not forcibly closed — they observe ctx themselves.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	srv := s.httpSrv
	s.mu.Unlock()
	if srv == nil {
		return nil
	}
	if err := srv.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// handleWS upgrades one HTTP request to a WebSocket and runs the
// connection lifetime loop. Each accepted connection is independent;
// cross-connection state lives in Handler / SessionManager / EventLedger.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Debug("ws upgrade failed", zap.Error(err))
		return
	}
	c := &client{
		conn:     conn,
		sessions: s.handler.Sessions,
		ledger:   s.handler.Ledger,
		handler:  s.handler,
		log:      s.log,
	}
	c.run(r.Context())
}
