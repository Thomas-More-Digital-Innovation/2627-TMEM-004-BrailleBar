package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	docURL := serviceURL("DOCUMENT_SERVICE_URL")
	jobURL := serviceURL("PRINT_JOB_SERVICE_URL")
	statusURL := serviceURL("NOTIFICATION_SERVICE_URL")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	srv := &http.Server{Addr: ":" + port, Handler: routes(docURL, jobURL, statusURL), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	slog.Info("listening", "service", "gateway", "port", port)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

// routes forwards only the listed public routes; everything else (like /documents/{id}/content) is a 404.
func routes(docURL, jobURL, statusURL *url.URL) http.Handler {
	docs := httputil.NewSingleHostReverseProxy(docURL)
	jobs := httputil.NewSingleHostReverseProxy(jobURL)
	status := httputil.NewSingleHostReverseProxy(statusURL) // also proxies the websocket upgrade

	mux := http.NewServeMux()
	// Docker and Railway use this to restart a crashed container.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.Handle("POST /documents/text", requireDevice(docs))
	mux.Handle("GET /jobs/{id}", requireDevice(jobs))
	mux.Handle("GET /jobs/{id}/pages", requireDevice(jobs))
	mux.Handle("POST /jobs/{id}/status", requireDevice(jobs))
	// Browsers can't set headers on a websocket, so the app sends ?deviceId= and we move it to the header.
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Device-Id", r.URL.Query().Get("deviceId"))
		requireDevice(status).ServeHTTP(w, r)
	})
	return mux
}

func serviceURL(key string) *url.URL {
	u, err := url.Parse(os.Getenv(key))
	if err != nil || u.Host == "" {
		slog.Error("config", "error", key+" is not a valid url")
		os.Exit(1)
	}
	return u
}

// requireDevice stops requests without a device id before they reach a service.
func requireDevice(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Device-Id")
		if id == "" || len(id) > 64 {
			http.Error(w, "missing or invalid X-Device-Id header", http.StatusBadRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}
