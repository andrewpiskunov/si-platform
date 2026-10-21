package main

import (
	"context"
	"siplatform/internal/retention"
	"testing"
	"time"
)

type blockingRetentionSweep struct {
	entered  chan struct{}
	canceled chan struct{}
}

func (s blockingRetentionSweep) Sweep(ctx context.Context) (retention.Stats, error) {
	close(s.entered)
	<-ctx.Done()
	close(s.canceled)
	return retention.Stats{}, ctx.Err()
}
func TestRetentionShutdownCancelsAndJoinsSweep(t *testing.T) {
	sweep := blockingRetentionSweep{make(chan struct{}), make(chan struct{})}
	stop := startRetention(context.Background(), sweep)
	select {
	case <-sweep.entered:
	case <-time.After(time.Second):
		t.Fatal("cleanup not started")
	}
	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not stop")
	}
	select {
	case <-sweep.canceled:
	default:
		t.Fatal("stop returned before canceled sweep completed")
	}
	stop()
}
