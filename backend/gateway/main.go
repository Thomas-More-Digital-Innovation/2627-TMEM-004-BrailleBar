package main

import (
	"log/slog"
	"net/http"
	"os"
)

func main() {
	mux := http.NewServeMux()
	// Docker and Railway use this to restart a crashed container.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	slog.Info("listening", "service", "gateway", "port", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
