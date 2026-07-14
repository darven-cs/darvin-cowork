package main

import (
	"encoding/json"
	"log"
	"net/http"

	"darvin-cowork/internal/config"
)

func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
}

func main() {
	cfg := config.Default()
	http.HandleFunc(config.HttpBasePath+"/hello", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"msg": "hello world",
		})
	}))

	log.Printf("[cowork] listening on %s", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, nil); err != nil {
		log.Fatalf("[cowork] server error: %v", err)
	}
}
