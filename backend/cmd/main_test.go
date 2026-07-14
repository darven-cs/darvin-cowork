package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHelloHandler(t *testing.T) {
	// 用与 main.go 相同的 handler 结构单独测试，不依赖 ListenAndServe
	handler := corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"msg": "hello world"})
	})

	t.Run("GET returns hello msg", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/hello", nil)
		w := httptest.NewRecorder()

		handler(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		if got := w.Header().Get("Content-Type"); got != "application/json" {
			t.Fatalf("expected application/json, got %q", got)
		}
		var body map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		if body["msg"] != "hello world" {
			t.Fatalf("expected msg=hello world, got %q", body["msg"])
		}
	})

	t.Run("OPTIONS preflight returns 204", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/api/hello", nil)
		w := httptest.NewRecorder()

		handler(w, req)

		if w.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", w.Code)
		}
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
			t.Fatalf("expected CORS *, got %q", got)
		}
	})
}