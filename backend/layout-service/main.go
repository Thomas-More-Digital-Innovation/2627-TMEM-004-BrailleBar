package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"BrailleBar/platform"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	docURL := os.Getenv("DOCUMENT_SERVICE_URL")
	if docURL == "" {
		fatal("config", errors.New("DOCUMENT_SERVICE_URL is not set"))
	}
	cellsPerLine := positiveInt("CELLS_PER_LINE")
	linesPerPage := positiveInt("LINES_PER_PAGE")

	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		fatal("database connection failed", err)
	}
	defer pool.Close()
	if err := platform.Migrate(ctx, pool); err != nil {
		fatal("platform migration failed", err)
	}
	if err := migrate(ctx, pool); err != nil {
		fatal("print_text table migration failed", err)
	}

	broker, err := platform.Connect(ctx, os.Getenv("RABBITMQ_URL"))
	if err != nil {
		fatal("broker connection failed", err)
	}
	defer broker.Close()

	consumer := &Consumer{
		Pool:         pool,
		Docs:         &DocClient{BaseURL: docURL, HTTP: &http.Client{Timeout: 5 * time.Second}},
		CellsPerLine: cellsPerLine,
		LinesPerPage: linesPerPage,
	}
	// If either loop stops because the broker is lost, exit so Docker restarts us. A normal shutdown is not an error.
	go func() {
		err := broker.Consume(ctx, "layout-service", []string{platform.DocumentUploadedEvent}, consumer.Handle)
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

	mux := http.NewServeMux()
	// Docker and Railway use this to restart a crashed container.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	srv := &http.Server{Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	slog.Info("listening", "service", "layout-service", "port", port, "cellsPerLine", cellsPerLine, "linesPerPage", linesPerPage)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fatal("server stopped", err)
	}
}

// positiveInt reads a required setting; Layout loops forever on 0 or less, so refuse to start instead.
func positiveInt(key string) int {
	n, err := strconv.Atoi(os.Getenv(key))
	if err != nil || n < 1 {
		fatal("config", errors.New(key+" must be a whole number of at least 1"))
	}
	return n
}

func fatal(msg string, err error) {
	slog.Error(msg, "error", err)
	os.Exit(1)
}
