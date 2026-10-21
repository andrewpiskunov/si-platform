package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"siplatform/internal/accounts"
	"siplatform/internal/ai"
	"siplatform/internal/blob"
	"siplatform/internal/ingest"
	"siplatform/internal/library"
	"siplatform/internal/media"
	"siplatform/internal/retention"
	"siplatform/internal/server"
	"siplatform/internal/storage/postgres"
	"siplatform/internal/themes"
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
	origin := os.Getenv("ALLOWED_ORIGIN")
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	aiConfig, err := aiOptions(os.Getenv, addr)
	if err != nil {
		return err
	}
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = ".local/data"
	}
	exportDirectory, err := prepareExportDirectory(dataDir)
	if err != nil {
		return err
	}
	objects, err := objectStore(ctx, dataDir)
	if err != nil {
		return err
	}
	auth := accounts.New(pool, accounts.Options{Origin: origin, SecureCookies: strings.HasPrefix(origin, "https://")})
	lib := library.New(pool)
	var processor *media.Processor
	if os.Getenv("MEDIA_DISABLED") != "true" {
		mediaDir := filepath.Join(dataDir, "media")
		if err = os.MkdirAll(mediaDir, 0700); err != nil {
			return err
		}
		processor, err = media.New(media.Options{FFmpeg: os.Getenv("MEDIA_FFMPEG"), FFprobe: os.Getenv("MEDIA_FFPROBE"), Sandbox: os.Getenv("MEDIA_SANDBOX"), Prlimit: os.Getenv("MEDIA_PRLIMIT"), Directory: mediaDir})
		if err != nil {
			return errors.New("media processor unavailable: install FFmpeg, ffprobe, bubblewrap and prlimit, configure MEDIA_* paths, or explicitly set MEDIA_DISABLED=true for text-only operation")
		}
	}
	uploads, err := ingest.New(pool, objects, lib, ingest.Options{Directory: filepath.Join(dataDir, "uploads"), Media: processor})
	if err != nil {
		return err
	}
	uploads.Start(ctx)
	defer uploads.Close()
	stopRetention := startRetention(ctx, retention.New(pool, objects))
	defer stopRetention()
	aiService, err := ai.New(pool, aiConfig)
	if err != nil {
		return errors.New("AI service configuration unavailable")
	}
	stopAI, err := aiService.StartWorkers(ctx)
	if err != nil {
		return errors.New("AI queue recovery unavailable")
	}
	defer stopAI()
	app := server.New(store, server.Options{AI: aiService, ExportDirectory: exportDirectory, Origin: origin, Accounts: auth, Library: lib, Objects: objects, Uploads: uploads, Themes: themes.New(pool)})
	defer app.Close()
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	httpServer := &http.Server{Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	served := make(chan error, 1)
	go func() { served <- httpServer.Serve(listener) }()
	log.Printf("SI platform API listening on %s", listener.Addr())
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
	stopAI()
	stopRetention()
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

func objectStore(ctx context.Context, dataDir string) (blob.Store, error) {
	if os.Getenv("S3_BUCKET") == "" {
		return blob.NewLocal(filepath.Join(dataDir, "objects"))
	}
	return blob.NewS3(ctx, blob.S3Config{RequireVersioning: true, Endpoint: os.Getenv("S3_ENDPOINT"), Region: os.Getenv("S3_REGION"), Bucket: os.Getenv("S3_BUCKET"), AccessKey: os.Getenv("S3_ACCESS_KEY"), SecretKey: os.Getenv("S3_SECRET_KEY"), SessionToken: os.Getenv("S3_SESSION_TOKEN"), PathStyle: os.Getenv("S3_PATH_STYLE") == "true", TempDir: os.Getenv("TMPDIR")})
}
