// Tests for the HTTP transport.

package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPConnect_SetsAlive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	tp := &HTTPTransport{URL: srv.URL}
	if err := tp.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tp.Close()
	if !tp.Alive() {
		t.Fatal("transport should be alive after Connect")
	}
}

func TestHTTP_NotConnected_RecvReturnsClosed(t *testing.T) {
	tp := &HTTPTransport{URL: "http://localhost:1"}
	_, err := tp.Recv(context.Background())
	if err != ErrTransportClosed {
		t.Fatalf("err = %v, want ErrTransportClosed", err)
	}
}

func TestHTTP_SendRecv_200EchoesResponse(t *testing.T) {
	want := `{"jsonrpc":"2.0","id":1,"result":{}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		if got := r.Header.Get("Accept"); !strings.Contains(got, "application/json") {
			t.Errorf("Accept = %q, want application/json", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(want))
	}))
	defer srv.Close()

	tp := &HTTPTransport{URL: srv.URL}
	if err := tp.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tp.Close()

	if err := tp.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1}`)); err != nil {
		t.Fatal(err)
	}
	frame, err := tp.Recv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(frame.Body) != want {
		t.Fatalf("body = %q, want %q", frame.Body, want)
	}
}

func TestHTTP_Send_500Errors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	tp := &HTTPTransport{URL: srv.URL}
	if err := tp.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tp.Close()

	err := tp.Send(context.Background(), []byte(`{}`))
	if err == nil {
		t.Fatal("expected error for 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("err = %v, want it to mention 500", err)
	}
	if tp.Alive() {
		t.Fatal("transport should be marked dead after server error")
	}
}

func TestHTTP_Send_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	tp := &HTTPTransport{URL: srv.URL, Timeout: 50 * time.Millisecond}
	if err := tp.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tp.Close()

	start := time.Now()
	err := tp.Send(context.Background(), []byte(`{}`))
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > 250*time.Millisecond {
		t.Fatalf("send took %v, want ~50ms (timeout-driven)", elapsed)
	}
}

func TestHTTP_SessionID_PropagatedOnSecondRequest(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			// First (initialize-like) request: server assigns a session id.
			w.Header().Set(httpHeaderSession, "session-abc")
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer srv.Close()

	tp := &HTTPTransport{URL: srv.URL}
	if err := tp.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tp.Close()

	if err := tp.Send(context.Background(), []byte(`{"id":1}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := tp.Recv(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Second request should now include the session id.
	if err := tp.Send(context.Background(), []byte(`{"id":2}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := tp.Recv(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("server saw %d calls, want 2", calls.Load())
	}
}

func TestHTTP_CustomHeaders_Applied(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	tp := &HTTPTransport{
		URL:     srv.URL,
		Headers: map[string]string{"Authorization": "Bearer test-token"},
	}
	if err := tp.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tp.Close()

	if err := tp.Send(context.Background(), []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := tp.Recv(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatalf("Authorization header = %q, want Bearer test-token", gotAuth)
	}
}

func TestHTTPClose_MarksDead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	tp := &HTTPTransport{URL: srv.URL}
	if err := tp.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := tp.Close(); err != nil {
		t.Fatal(err)
	}
	if tp.Alive() {
		t.Fatal("transport should be dead after Close")
	}
	if err := tp.Close(); err != nil {
		t.Fatalf("second Close must be a no-op, got %v", err)
	}
}
