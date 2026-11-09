package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"BrailleBar/platform"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	broker, err := platform.Connect(ctx, os.Getenv("RABBITMQ_URL"))
	if err != nil {
		fatal("broker connection failed", err)
	}
	defer broker.Close()

	hub := NewHub()
	// Every replica hears every event, because only one of them holds the device's socket.
	go func() {
		err := broker.Subscribe(ctx, []string{platform.PrintJobCreatedEvent, platform.PrintJobStatusEvent}, hub.Handle)
		if ctx.Err() == nil {
			fatal("consumer stopped", err)
		}
	}()

	mux := http.NewServeMux()
	// Docker and Railway use this to restart a crashed container.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /status", hub.ServeWS)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	srv := &http.Server{
		Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 10 * time.Second,
		// Shutdown doesn't wait for or close websockets; ending their context on SIGTERM does.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	slog.Info("listening", "service", "notification-service", "port", port)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fatal("server stopped", err)
	}
}

func fatal(msg string, err error) {
	slog.Error(msg, "error", err)
	os.Exit(1)
}
