package main

import (
	"context"
	"log"
	"time"

	"siplatform/internal/retention"
)

type retentionSweeper interface {
	Sweep(context.Context) (retention.Stats, error)
}

// One bounded pass at a time; shutdown cancels and joins in-flight cleanup.
func startRetention(parent context.Context, service retentionSweeper) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			pass, stop := context.WithTimeout(ctx, 30*time.Second)
			stats, err := service.Sweep(pass)
			stop()
			if stats != (retention.Stats{}) {
				log.Printf("retention sweep: %+v", stats)
			}
			if err != nil && ctx.Err() == nil {
				log.Print("retention sweep incomplete; queued cleanup will be retried")
			}
			timer := time.NewTimer(time.Minute)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}
