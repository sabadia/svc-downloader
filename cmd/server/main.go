package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/sabadia/svc-downloader/internal/api"
	"github.com/sabadia/svc-downloader/internal/api/deps"
	"github.com/sabadia/svc-downloader/internal/config"
	"github.com/sabadia/svc-downloader/internal/events"
	"github.com/sabadia/svc-downloader/internal/filestore"
	"github.com/sabadia/svc-downloader/internal/models"
	"github.com/sabadia/svc-downloader/internal/planner"
	"github.com/sabadia/svc-downloader/internal/ratelimit"
	"github.com/sabadia/svc-downloader/internal/repository"
	"github.com/sabadia/svc-downloader/internal/service"
	"github.com/sabadia/svc-downloader/internal/transport"
	"github.com/sabadia/svc-downloader/internal/validation"
)

func main() {
	cfg := config.Default()
	if p := os.Getenv("PORT"); p != "" {
		// ignore parse error, default used
		if port, err := strconv.Atoi(p); err == nil {
			cfg.HTTPPort = port
		}
	}
	if grl := os.Getenv("GLOBAL_RATE_LIMIT_BPS"); grl != "" {
		if v, err := strconv.ParseInt(grl, 10, 64); err == nil {
			cfg.GlobalRateLimitBPS = v
		}
	}

	repo, err := repository.NewBadgerRepository(cfg.BadgerDir)
	if err != nil {
		log.Fatalf("failed to init repository: %v", err)
	}
	defer func(repo *repository.BadgerRepository) {
		err := repo.Close()
		if err != nil {
			log.Printf("warning: failed to close repository: %v", err)
		}
	}(repo)

	publisher := events.NewInMemoryPublisher()
	validator := validation.NoopValidator{}
	segPlanner := planner.SimplePlanner{}
	ratelimiter := &ratelimit.SimpleRateLimiter{}
	transportClient := transport.NewHTTPClient(0)
	fileStore := filestore.NewLocalFileStore()

	// Ensure default queue exists
	if _, err := repo.GetQueue(context.Background(), models.DefaultQueueName); err != nil {
		_ = repo.SaveQueue(context.Background(), &models.Queue{ID: models.DefaultQueueName, Name: models.DefaultQueueName, Concurrency: 32, Default: true})
	}

	// Apply global rate limit if configured
	if cfg.GlobalRateLimitBPS > 0 {
		ratelimiter.SetLimit("global", cfg.GlobalRateLimitBPS)
	}

	downloadSvc := service.NewDownloadService(service.DownloadDeps{
		Repo:        repo,
		Publisher:   publisher,
		Validator:   validator,
		Planner:     segPlanner,
		RateLimiter: ratelimiter,
		FileStore:   fileStore,
		Transport:   transportClient,
	})
	queueSvc := service.NewQueueService(repo)

	container := deps.New(downloadSvc, queueSvc, publisher)
	handler, apiDef := api.NewServer(container)
	_ = apiDef // available for future CLI usage

	// start worker manager
	workerMgr := service.NewWorkerManager(downloadSvc, repo)
	ctx, cancelWorkers := context.WithCancel(context.Background())
	defer cancelWorkers()
	workerMgr.Start(ctx)

	addr := ":" + os.Getenv("PORT")
	if cfg.HTTPPort != 0 && os.Getenv("PORT") == "" {
		addr = ":" + itoa(cfg.HTTPPort)
	}
	srv := &http.Server{Addr: addr, Handler: handler}

	go func() {
		log.Printf("svc-downloader listening on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.GracefulSecs)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func itoa(i int) string { return fmt.Sprintf("%d", i) }
