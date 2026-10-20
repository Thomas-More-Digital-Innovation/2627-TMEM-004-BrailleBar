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
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil))) // structured JSON logs (see Readme)

	// ctx is cancelled when Docker stops the container, so everything below can shut down cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		fatal("database connection failed", err)
	}
	defer pool.Close()

	if err := platform.Migrate(ctx, pool); err != nil {
		fatal("platform migration failed", err)
	}
	if err := migrate(ctx, pool); err != nil {
		fatal("document table migration failed", err)
	}

	broker, err := platform.Connect(ctx, os.Getenv("RABBITMQ_URL"))
	if err != nil {
		fatal("broker connection failed", err)
	}
	defer broker.Close()

	// Publishes the events the handler puts in the outbox. If it stops because the broker is lost,
	// exit so Docker restarts us. A normal shutdown (ctx cancelled) is not an error.
	go func() {
		err := platform.RunOutbox(ctx, pool, broker)
		if ctx.Err() == nil {
			fatal("outbox stopped", err)
		}
	}()

	mux := http.NewServeMux()
	// Docker and Railway use this to restart a crashed container.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("POST /documents/text", (&Handler{Pool: pool}).SubmitText)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	srv := &http.Server{Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx) // lets requests in progress finish
	}()

	slog.Info("listening", "service", "document-service", "port", port)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fatal("server stopped", err)
	}
}

func fatal(msg string, err error) {
	slog.Error(msg, "error", err)
	os.Exit(1)
}
