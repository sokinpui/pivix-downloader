package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sokinpui/pixiv-downloader/internal/database"
	"github.com/sokinpui/pixiv-downloader/internal/engine"
	"github.com/sokinpui/pixiv-downloader/internal/handler"
	"github.com/sokinpui/pixiv-downloader/internal/pixiv"
	"github.com/sokinpui/pixiv-downloader/internal/repository"
	"github.com/sokinpui/pixiv-downloader/internal/service"
)

func main() {
	port := flag.Int("port", 8080, "HTTP server port")
	dbPath := flag.String("db", "./pixiv-downloader.db", "SQLite database file path")
	sessionID := flag.String("session", "", "Pixiv PHPSESSID (optional override)")
	userID := flag.String("user-id", "", "Pixiv User ID (optional override)")
	proxyAddr := flag.String("proxy", "", "Optional HTTP/SOCKS5 proxy")
	outputDir := flag.String("output-dir", "./downloads", "Directory to save downloaded artworks")
	maxWorkers := flag.Int("workers", 2, "Max concurrent download workers")
	flag.Parse()

	log.Println("[Init] Initializing SQLite database...")
	db, err := database.InitDB(*dbPath)
	if err != nil {
		log.Fatalf("failed to initialize database: %v", err)
	}
	defer db.Close()

	settingsRepo := repository.NewSettingsRepository(db)
	artworkRepo := repository.NewArtworkRepository(db)
	taskRepo := repository.NewTaskRepository(db)

	// Seed or load settings
	if *sessionID != "" {
		_ = settingsRepo.Set("session_id", *sessionID)
	}
	if *userID != "" {
		_ = settingsRepo.Set("user_id", *userID)
	}
	if *proxyAddr != "" {
		_ = settingsRepo.Set("proxy", *proxyAddr)
	}
	if *outputDir != "" {
		_ = settingsRepo.Set("download_dir", *outputDir)
	}
	_ = settingsRepo.Set("max_workers", fmt.Sprintf("%d", *maxWorkers))

	dbSession, _ := settingsRepo.Get("session_id")
	dbProxy, _ := settingsRepo.Get("proxy")
	dbOutput, _ := settingsRepo.Get("download_dir")
	if dbOutput == "" {
		dbOutput = *outputDir
	}

	client, err := pixiv.NewPixivClient(dbSession, dbProxy, 45*time.Second)
	if err != nil {
		log.Fatalf("failed to initialize Pixiv client: %v", err)
	}

	eh := service.NewEventHub()

	downloadEngine := engine.NewDownloadEngine(client, taskRepo, artworkRepo, eh, dbOutput, *maxWorkers)
	downloadEngine.Start()
	defer downloadEngine.Stop()

	enqueueFunc := func(taskID int64) {
		downloadEngine.EnqueueTask(taskID)
	}

	syncService := service.NewSyncService(client, artworkRepo, taskRepo, eh, enqueueFunc)
	submitService := service.NewSubmissionService(client, artworkRepo, taskRepo, eh, enqueueFunc)

	hnd := handler.NewHandler(
		settingsRepo,
		artworkRepo,
		taskRepo,
		eh,
		syncService,
		submitService,
		downloadEngine,
		func(newSession, newProxy string) error {
			newClient, err := pixiv.NewPixivClient(newSession, newProxy, 45*time.Second)
			if err != nil {
				return err
			}
			_ = newClient
			return nil
		},
	)

	mux := http.NewServeMux()
	hnd.RegisterRoutes(mux)

	addr := fmt.Sprintf(":%d", *port)
	srv := &http.Server{
		Addr:    addr,
		Handler: handler.CORS(mux),
	}

	// Resume pending tasks on startup
	go func() {
		time.Sleep(500 * time.Millisecond)
		pendingTasks, err := taskRepo.ListPending()
		if err != nil {
			log.Printf("[Engine] Failed to list pending tasks on startup: %v", err)
			return
		}
		if len(pendingTasks) > 0 {
			log.Printf("[Engine] Resuming %d pending/downloading task(s) from database...", len(pendingTasks))
			for _, t := range pendingTasks {
				downloadEngine.EnqueueTask(t.ID)
			}
		}
	}()

	go func() {
		log.Printf("[Server] Pixiv Downloader backend started on http://0.0.0.0%s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	log.Println("[Server] Shutting down server gracefully...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server forced to shutdown: %v", err)
	}

	log.Println("[Server] Server stopped successfully.")
}
