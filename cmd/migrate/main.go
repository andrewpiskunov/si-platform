package main

import (
	"context"
	"log"
	"os"
	"time"

	"siplatform/internal/storage/postgres"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool, err := postgres.Connect(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer pool.Close()
	if err = postgres.Migrate(ctx, pool); err != nil {
		return err
	}
	log.Print("database migrations applied")
	return nil
}
