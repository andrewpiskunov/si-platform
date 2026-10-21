package main

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"log"
	"net"
	"net/http"
	"siplatform/internal/telemetry"
	"strconv"
	"sync"
	"time"
)

type metricsConfig struct{ Address, Token string }

func metricsOptions(env func(string) string) (metricsConfig, error) {
	c := metricsConfig{Address: env("METRICS_ADDR"), Token: env("METRICS_TOKEN")}
	if c.Address == "" {
		return c, nil
	}
	_, port, e := net.SplitHostPort(c.Address)
	if e != nil {
		return c, errors.New("invalid METRICS_ADDR")
	}
	n, e := strconv.Atoi(port)
	if e != nil || n < 0 || n > 65535 {
		return c, errors.New("invalid METRICS_ADDR")
	}
	if e = telemetry.ValidateToken(c.Token); e != nil {
		return c, errors.New("METRICS_TOKEN must contain 32 to 256 printable non-space ASCII characters")
	}
	return c, nil
}

type metricsServer struct {
	listener net.Listener
	server   *http.Server
	stop     func()
	once     sync.Once
	done     chan struct{}
}

func (s *metricsServer) Address() string { return s.listener.Addr().String() }
func (s *metricsServer) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		s.stop()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := s.server.Shutdown(ctx); e != nil {
			_ = s.server.Close()
		}
		<-s.done
	})
}
func startMetrics(ctx context.Context, m *telemetry.Metrics, pool *pgxpool.Pool, c metricsConfig) (*metricsServer, error) {
	if c.Address == "" {
		return nil, nil
	}
	handler, e := m.Handler(c.Token)
	if e != nil {
		return nil, e
	}
	listener, e := net.Listen("tcp", c.Address)
	if e != nil {
		return nil, errors.New("metrics listener unavailable")
	}
	s := &metricsServer{listener: listener, server: &http.Server{Handler: handler, ReadHeaderTimeout: 3 * time.Second, WriteTimeout: 6 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 4096}, stop: m.WatchDatabase(ctx, pool), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		if e := s.server.Serve(listener); e != nil && !errors.Is(e, http.ErrServerClosed) {
			log.Print("metrics listener stopped unexpectedly")
		}
	}()
	return s, nil
}
