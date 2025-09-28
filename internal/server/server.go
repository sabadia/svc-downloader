package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/sabadia/svc-downloader/internal/api"
	"github.com/sabadia/svc-downloader/internal/api/deps"
	"github.com/sabadia/svc-downloader/internal/auth"
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

type Server struct {
	cfg       config.Config
	repo      *repository.BadgerRepository
	workerMgr *service.WorkerManager
	httpSrv   *http.Server
	logger    *slog.Logger
}

func New(cfg config.Config) (*Server, error) {
	// Initialize structured logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	repo, err := repository.NewBadgerRepository(cfg.BadgerDir)
	if err != nil {
		logger.Error("Failed to create repository", "error", err, "badger_dir", cfg.BadgerDir)
		return nil, err
	}

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
	h, _ := api.NewServer(container)

	// Apply authentication middleware if enabled
	if cfg.EnableAuth && cfg.APIKey != "" {
		authMiddleware := auth.NewAPIKeyAuth(cfg.APIKey)
		h = authMiddleware.HumaMiddleware()(h)
	}

	addr := ":" + fmt.Sprintf("%d", resolvePort(cfg))
	httpSrv := &http.Server{Addr: addr, Handler: h}

	workerCfg := service.WorkerConfig{
		TickInterval:         cfg.WorkerTickInterval,
		StaleDownloadTimeout: cfg.StaleDownloadTimeout,
	}
	workerMgr := service.NewWorkerManagerWithConfig(downloadSvc, repo, workerCfg)

	logger.Info("Server initialized successfully",
		"port", resolvePort(cfg),
		"data_dir", cfg.DataDir,
		"badger_dir", cfg.BadgerDir,
		"auth_enabled", cfg.EnableAuth)

	return &Server{cfg: cfg, repo: repo, workerMgr: workerMgr, httpSrv: httpSrv, logger: logger}, nil
}

func (s *Server) Addr() string { return s.httpSrv.Addr }

// RunForeground starts the server and blocks until ctx is done, then performs graceful shutdown.
func (s *Server) RunForeground(ctx context.Context) error {
	ctx, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()
	s.workerMgr.Start(ctx)

	// start server
	go func() {
		s.logger.Info("Starting HTTP server", "addr", s.httpSrv.Addr)
		if err := s.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("Server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()

	// Graceful shutdown: pause running downloads before stopping
	s.logger.Info("Graceful shutdown initiated, pausing running downloads")
	if err := s.pauseRunningDownloads(ctx); err != nil {
		s.logger.Warn("Failed to pause some downloads", "error", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.GracefulSecs)
	defer cancel()
	_ = s.httpSrv.Shutdown(shutdownCtx)
	return nil
}

// pauseRunningDownloads transitions all running downloads to paused state
func (s *Server) pauseRunningDownloads(ctx context.Context) error {
	// Get all running downloads
	downloads, err := s.repo.ListDownloads(ctx, models.ListDownloadsOptions{
		Statuses: []models.DownloadStatus{models.StatusRunning},
	}, 1000, 0) // Get up to 1000 running downloads

	if err != nil {
		return err
	}

	// Pause each running download
	for _, download := range downloads {
		download.Status = models.StatusPaused
		download.UpdatedAt = time.Now().UTC()
		if err := s.repo.UpdateDownload(ctx, &download); err != nil {
			s.logger.Error("Failed to pause download",
				"download_id", download.ID,
				"error", err)
		}
	}

	s.logger.Info("Paused running downloads", "count", len(downloads))
	return nil
}

// Close closes server and repository quickly without graceful handling.
func (s *Server) Close() error {
	_ = s.httpSrv.Close()
	return s.repo.Close()
}

func resolvePort(cfg config.Config) int {
	if cfg.HTTPPort != 0 {
		return cfg.HTTPPort
	}
	return 8089
}
