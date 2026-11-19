package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"BrailleBar/platform"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	defer platform.InitTracing(ctx, "print-job-service")()
	if err := platform.LoadEncryptionKey(); err != nil {
		fatal("encryption key", err)
	}

	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		fatal("database connection failed", err)
	}
	defer pool.Close()
	if err := platform.Migrate(ctx, pool); err != nil {
		fatal("platform migration failed", err)
	}
	if err := migrate(ctx, pool); err != nil {
		fatal("print_job table migration failed", err)
	}

	go func() {
		err := platform.RunRetention(ctx, pool, "print_job")
		if ctx.Err() == nil {
			fatal("retention stopped", err)
		}
	}()

	broker, err := platform.Connect(ctx, os.Getenv("RABBITMQ_URL"))
	if err != nil {
		fatal("broker connection failed", err)
	}
	defer broker.Close()

	consumer := &Consumer{Pool: pool}
	// If either loop stops because the broker is lost, exit so Docker restarts us. A normal shutdown is not an error.
	go func() {
		err := broker.Consume(ctx, "print-job-service", []string{platform.DocumentLayoutCompletedEvent}, consumer.Handle)
		if ctx.Err() == nil {
			fatal("consumer stopped", err)
		}
	}()
	go func() {
		err := platform.RunOutbox(ctx, pool, broker)
		if ctx.Err() == nil {
			fatal("outbox stopped", err)
		}
	}()

	h := &Handler{Pool: pool}
	mux := http.NewServeMux()
	// Docker and Railway use this to restart a crashed container.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /jobs", h.OpenJobs)
	mux.HandleFunc("GET /jobs/{id}", h.GetJob)
	mux.HandleFunc("GET /jobs/{id}/pages", h.GetPages)
	mux.HandleFunc("POST /jobs/{id}/status", h.ReportStatus)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	srv := &http.Server{Addr: ":" + port, Handler: platform.TraceHTTP(mux), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	slog.Info("listening", "service", "print-job-service", "port", port)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fatal("server stopped", err)
	}
}

func fatal(msg string, err error) {
	slog.Error(msg, "error", err)
	os.Exit(1)
}
