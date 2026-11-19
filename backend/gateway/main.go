package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"BrailleBar/platform"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	defer platform.InitTracing(ctx, "gateway")()

	docURL := serviceURL("DOCUMENT_SERVICE_URL")
	jobURL := serviceURL("PRINT_JOB_SERVICE_URL")
	statusURL := serviceURL("NOTIFICATION_SERVICE_URL")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	perMinute, err := strconv.Atoi(os.Getenv("RATE_LIMIT_PER_MINUTE"))
	if err != nil || perMinute < 1 {
		slog.Error("config", "error", "RATE_LIMIT_PER_MINUTE must be a positive number")
		os.Exit(1)
	}
	srv := &http.Server{Addr: ":" + port, Handler: platform.TraceHTTP(routes(docURL, jobURL, statusURL, perMinute)), ReadHeaderTimeout: 10 * time.Second}
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
func routes(docURL, jobURL, statusURL *url.URL, textsPerMinute int) http.Handler {
	docs := httputil.NewSingleHostReverseProxy(docURL)
	jobs := httputil.NewSingleHostReverseProxy(jobURL)
	status := httputil.NewSingleHostReverseProxy(statusURL) // also proxies the websocket upgrade

	mux := http.NewServeMux()
	// Docker and Railway use this to restart a crashed container.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	limited := rateLimit(textsPerMinute) // text and files count towards the same limit
	mux.Handle("POST /documents/text", requireDevice(limited(docs)))
	mux.Handle("POST /documents/file", requireDevice(limited(docs)))
	mux.Handle("GET /jobs", requireDevice(jobs))
	mux.Handle("GET /jobs/{id}", requireDevice(jobs))
	mux.Handle("GET /jobs/{id}/pages", requireDevice(jobs))
	mux.Handle("POST /jobs/{id}/status", requireDevice(jobs))
	// Browsers can't set headers on a websocket, so the app sends ?deviceId= and we move it to the header.
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Device-Id", r.URL.Query().Get("deviceId"))
		requireDevice(status).ServeHTTP(w, r)
	})
	return limitBody(mux)
}

// The Document Service decides what is too much text; this only keeps absurd uploads away from the services.
const maxBodyBytes = 21 << 20

func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxBodyBytes {
			http.Error(w, "request is too large", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		next.ServeHTTP(w, r)
	})
}

// rateLimit allows perMinute requests per client IP in each clock minute. The device id can't be the key: a client
// makes that up itself. Starting a new map every minute means it can't grow without bound.
// ponytail: RemoteAddr is the proxy once deployed on Railway, so all users would share one limit; use its forwarded client IP then.
func rateLimit(perMinute int) func(http.Handler) http.Handler {
	var mu sync.Mutex
	var window time.Time
	counts := map[string]int{}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				ip = r.RemoteAddr
			}
			mu.Lock()
			if now := time.Now().Truncate(time.Minute); !now.Equal(window) {
				window, counts = now, map[string]int{}
			}
			counts[ip]++
			over := counts[ip] > perMinute
			mu.Unlock()
			if over {
				w.Header().Set("Retry-After", "60")
				http.Error(w, "too many requests", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
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
