package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"siplatform/internal/server"
	"siplatform/internal/storage/postgres"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := postgres.Connect(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer pool.Close()
	store := postgres.New(pool)
	lease, err := store.Own(ctx)
	if err != nil {
		return err
	}
	defer lease.Close()
	recovery, cancel := context.WithTimeout(ctx, 30*time.Second)
	err = store.Recover(recovery)
	cancel()
	if err != nil {
		return err
	}
	app := server.New(store, server.Options{Origin: os.Getenv("ALLOWED_ORIGIN")})
	defer app.Close()
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	httpServer := &http.Server{Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	served := make(chan error, 1)
	go func() { served <- httpServer.Serve(listener) }()
	log.Printf("game core listening on %s", listener.Addr())
	var outcome error
	select {
	case <-ctx.Done():
	case <-lease.Done():
		outcome = errors.New("database ownership lost; stopping")
	case err := <-served:
		if !errors.Is(err, http.ErrServerClosed) {
			outcome = err
		}
	}
	app.Close()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = httpServer.Shutdown(shutdown); err != nil {
		_ = httpServer.Close()
		if outcome == nil {
			outcome = err
		}
	}
	return outcome
}
