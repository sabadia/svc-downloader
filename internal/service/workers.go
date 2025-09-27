package service

import (
	"context"
	"time"

	"github.com/sabadia/svc-downloader/internal/models"
)

type WorkerManager struct {
	svc  *DownloadServiceImpl
	repo models.Repository
	stop chan struct{}
}

func NewWorkerManager(svc *DownloadServiceImpl, repo models.Repository) *WorkerManager {
	return &WorkerManager{svc: svc, repo: repo, stop: make(chan struct{})}
}

func (w *WorkerManager) Start(ctx context.Context) {
	go w.loop(ctx)
}

func (w *WorkerManager) Stop() { close(w.stop) }

func (w *WorkerManager) loop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

func (w *WorkerManager) tick(ctx context.Context) {
	queues, err := w.repo.ListQueues(ctx)
	if err != nil {
		return
	}
	// Recover stale running downloads (no updates for 5 minutes)
	const staleAfter = 5 * time.Minute
	runningList, _ := w.repo.ListDownloads(ctx, models.ListDownloadsOptions{Statuses: []models.DownloadStatus{models.StatusRunning}}, 1000, 0)
	for i := range runningList {
		d := runningList[i]
		if d.UpdatedAt.IsZero() || time.Since(d.UpdatedAt) > staleAfter {
			// Requeue
			d.Status = models.StatusQueued
			_ = w.repo.UpdateDownload(ctx, &d)
		}
	}
	for i := range queues {
		q := queues[i]
		if q.Paused {
			continue
		}
		// apply queue rate limit if configured
		if w.svc.deps.RateLimiter != nil && q.RateLimit > 0 {
			w.svc.deps.RateLimiter.SetLimit(q.ID, q.RateLimit)
		}
		concurrency := q.Concurrency
		if concurrency <= 0 {
			concurrency = 1
		}
		// Count running and queued for stats and scheduling
		running, _ := w.repo.CountDownloads(ctx, models.ListDownloadsOptions{Statuses: []models.DownloadStatus{models.StatusRunning}, QueueIDs: []string{q.ID}})
		queued, _ := w.repo.CountDownloads(ctx, models.ListDownloadsOptions{Statuses: []models.DownloadStatus{models.StatusQueued}, QueueIDs: []string{q.ID}})
		completed, _ := w.repo.CountDownloads(ctx, models.ListDownloadsOptions{Statuses: []models.DownloadStatus{models.StatusCompleted}, QueueIDs: []string{q.ID}})
		failed, _ := w.repo.CountDownloads(ctx, models.ListDownloadsOptions{Statuses: []models.DownloadStatus{models.StatusFailed}, QueueIDs: []string{q.ID}})
		_ = w.repo.SaveQueueStats(ctx, models.QueueStats{QueueID: q.ID, NumPending: queued, NumRunning: running, NumCompleted: completed, NumFailed: failed})

		toStart := concurrency - running
		if toStart <= 0 {
			continue
		}
		opts := models.ListDownloadsOptions{Statuses: []models.DownloadStatus{models.StatusQueued}, QueueIDs: []string{q.ID}, OrderBy: "priority", OrderDesc: true}
		list, err := w.repo.ListDownloads(ctx, opts, toStart, 0)
		if err != nil {
			continue
		}
		for j := range list {
			_ = w.svc.Start(ctx, list[j].ID)
		}
	}
}
