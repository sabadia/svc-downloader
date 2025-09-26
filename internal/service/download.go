package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sabadia/svc-downloader/internal/models"
)

type DownloadDeps struct {
	Repo        models.Repository
	Publisher   models.EventPublisher
	Validator   models.Validator
	Planner     models.SegmentPlanner
	RateLimiter models.RateLimiter
	FileStore   models.FileStore
	Transport   models.TransportClient
}

type DownloadServiceImpl struct {
	deps    DownloadDeps
	runner  *DownloadRunner
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func NewDownloadService(deps DownloadDeps) *DownloadServiceImpl {
	return &DownloadServiceImpl{deps: deps, runner: NewDownloadRunner(deps), cancels: make(map[string]context.CancelFunc)}
}

func (s *DownloadServiceImpl) Enqueue(ctx context.Context, req models.EnqueueDownloadRequest) (models.EnqueueDownloadResponse, error) {
	if req.URL == "" && (req.Request == nil || req.Request.URL == "") {
		return models.EnqueueDownloadResponse{}, errors.New("url is required")
	}
	if req.Request == nil {
		req.Request = &models.RequestOptions{URL: req.URL}
	}
	if s.deps.Validator != nil {
		if err := s.deps.Validator.ValidateRequest(*req.Request); err != nil {
			return models.EnqueueDownloadResponse{}, err
		}
	}
	id := uuid.NewString()
	now := time.Now().UTC()
	d := &models.Download{
		ID:        id,
		URL:       req.Request.URL,
		QueueID:   req.QueueID,
		Priority:  req.Priority,
		Tags:      req.Tags,
		Request:   req.Request,
		Config:    req.Config,
		File:      req.File,
		Status:    models.StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if d.QueueID == "" {
		d.QueueID = models.DefaultQueueName
	}
	if err := s.deps.Repo.CreateDownload(ctx, d); err != nil {
		return models.EnqueueDownloadResponse{}, err
	}
	_ = s.publish(ctx, models.EventEnqueued, *d, nil)
	return models.EnqueueDownloadResponse{ID: d.ID, QueueID: d.QueueID, Status: d.Status}, nil
}

func (s *DownloadServiceImpl) Start(ctx context.Context, id string) error {
	// Prevent duplicate starts
	s.mu.Lock()
	if _, exists := s.cancels[id]; exists {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	// Configure rate limit for this download if specified
	if s.deps.RateLimiter != nil {
		if d, err := s.deps.Repo.GetDownload(ctx, id); err == nil && d.Config != nil && d.Config.RateLimit > 0 {
			s.deps.RateLimiter.SetLimit(id, d.Config.RateLimit)
		}
	}
	// Transition to running and spawn runner
	if err := s.updateStatus(ctx, id, models.StatusRunning, func(d *models.Download) {
		now := time.Now().UTC()
		d.StartedAt = &now
	}); err != nil {
		return err
	}
	ctxRun, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancels[id] = cancel
	s.mu.Unlock()
	go func() {
		s.runner.Run(ctxRun, id)
		s.mu.Lock()
		delete(s.cancels, id)
		s.mu.Unlock()
	}()
	return nil
}

func (s *DownloadServiceImpl) Pause(ctx context.Context, id string) error {
	s.mu.Lock()
	if c, ok := s.cancels[id]; ok {
		c()
		delete(s.cancels, id)
	}
	s.mu.Unlock()
	return s.updateStatus(ctx, id, models.StatusPaused, nil)
}

func (s *DownloadServiceImpl) Resume(ctx context.Context, id string) error {
	// Move to running and start the runner similar to Start
	if err := s.updateStatus(ctx, id, models.StatusRunning, nil); err != nil {
		return err
	}
	ctxRun, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancels[id] = cancel
	s.mu.Unlock()
	go func() {
		s.runner.Run(ctxRun, id)
		s.mu.Lock()
		delete(s.cancels, id)
		s.mu.Unlock()
	}()
	return nil
}

func (s *DownloadServiceImpl) Cancel(ctx context.Context, id string) error {
	s.mu.Lock()
	if c, ok := s.cancels[id]; ok {
		c()
		delete(s.cancels, id)
	}
	s.mu.Unlock()
	return s.updateStatus(ctx, id, models.StatusCancelled, nil)
}

func (s *DownloadServiceImpl) Remove(ctx context.Context, id string, deleteFiles bool) error {
	d, err := s.deps.Repo.GetDownload(ctx, id)
	if err != nil {
		return err
	}
	if err := s.deps.Repo.DeleteDownload(ctx, id); err != nil {
		return err
	}
	if deleteFiles && s.deps.FileStore != nil {
		_ = s.deps.FileStore.RemoveDownloadFiles(ctx, d)
	}
	_ = s.publish(ctx, models.EventCancelled, *d, map[string]any{"deleted": true})
	return nil

}

func (s *DownloadServiceImpl) Get(ctx context.Context, id string) (*models.Download, error) {
	return s.deps.Repo.GetDownload(ctx, id)
}

func (s *DownloadServiceImpl) List(ctx context.Context, options models.ListDownloadsOptions, limit, offset int) ([]models.Download, error) {
	return s.deps.Repo.ListDownloads(ctx, options, limit, offset)
}

func (s *DownloadServiceImpl) Count(ctx context.Context, options models.ListDownloadsOptions) (int, error) {
	return s.deps.Repo.CountDownloads(ctx, options)
}

func (s *DownloadServiceImpl) UpdateConfig(ctx context.Context, id string, cfg models.DownloadConfig) error {
	if s.deps.Validator != nil {
		if err := s.deps.Validator.ValidateConfig(cfg); err != nil {
			return err
		}
	}
	return s.deps.Repo.RunInTx(ctx, func(ctx context.Context, tx models.Repository) error {
		d, err := tx.GetDownload(ctx, id)
		if err != nil {
			return err
		}
		if d.Config == nil {
			d.Config = &models.DownloadConfig{}
		}
		*d.Config = cfg
		return tx.UpdateDownload(ctx, d)
	})
}

func (s *DownloadServiceImpl) UpdateRequest(ctx context.Context, id string, req models.RequestOptions) error {
	if s.deps.Validator != nil {
		if err := s.deps.Validator.ValidateRequest(req); err != nil {
			return err
		}
	}
	return s.deps.Repo.RunInTx(ctx, func(ctx context.Context, tx models.Repository) error {
		d, err := tx.GetDownload(ctx, id)
		if err != nil {
			return err
		}
		if d.Request == nil {
			d.Request = &models.RequestOptions{}
		}
		*d.Request = req
		return tx.UpdateDownload(ctx, d)
	})
}

func (s *DownloadServiceImpl) SetPriority(ctx context.Context, id string, priority int) error {
	return s.deps.Repo.RunInTx(ctx, func(ctx context.Context, tx models.Repository) error {
		d, err := tx.GetDownload(ctx, id)
		if err != nil {
			return err
		}
		d.Priority = priority
		return tx.UpdateDownload(ctx, d)
	})
}

func (s *DownloadServiceImpl) AddTags(ctx context.Context, id string, tags ...string) error {
	return s.deps.Repo.RunInTx(ctx, func(ctx context.Context, tx models.Repository) error {
		d, err := tx.GetDownload(ctx, id)
		if err != nil {
			return err
		}
		existing := make(map[string]struct{})
		for _, t := range d.Tags {
			existing[strings.ToLower(t)] = struct{}{}
		}
		for _, t := range tags {
			if t == "" {
				continue
			}
			lt := strings.ToLower(t)
			if _, ok := existing[lt]; !ok {
				d.Tags = append(d.Tags, t)
				existing[lt] = struct{}{}
			}
		}
		return tx.UpdateDownload(ctx, d)
	})
}

func (s *DownloadServiceImpl) RemoveTags(ctx context.Context, id string, tags ...string) error {
	return s.deps.Repo.RunInTx(ctx, func(ctx context.Context, tx models.Repository) error {
		d, err := tx.GetDownload(ctx, id)
		if err != nil {
			return err
		}
		remove := make(map[string]struct{})
		for _, t := range tags {
			remove[strings.ToLower(t)] = struct{}{}
		}
		var filtered []string
		for _, t := range d.Tags {
			if _, ok := remove[strings.ToLower(t)]; !ok {
				filtered = append(filtered, t)
			}
		}
		d.Tags = filtered
		return tx.UpdateDownload(ctx, d)
	})
}

func (s *DownloadServiceImpl) AssignQueue(ctx context.Context, id string, queueID string) error {
	return s.deps.Repo.RunInTx(ctx, func(ctx context.Context, tx models.Repository) error {
		d, err := tx.GetDownload(ctx, id)
		if err != nil {
			return err
		}
		d.QueueID = queueID
		return tx.UpdateDownload(ctx, d)
	})
}

func (s *DownloadServiceImpl) updateStatus(ctx context.Context, id string, status models.DownloadStatus, mutate func(*models.Download)) error {
	return s.deps.Repo.RunInTx(ctx, func(ctx context.Context, tx models.Repository) error {
		d, err := tx.GetDownload(ctx, id)
		if err != nil {
			return err
		}
		d.Status = status
		if mutate != nil {
			mutate(d)
		}
		d.UpdatedAt = time.Now().UTC()
		if err := tx.UpdateDownload(ctx, d); err != nil {
			return err
		}
		err = s.publish(ctx, mapStatusToEvent(status), *d, nil)
		if err != nil {
			return err
		}
		return nil
	})
}

func (s *DownloadServiceImpl) publish(ctx context.Context, typ models.DownloadEventType, d models.Download, data map[string]any) error {
	if s.deps.Publisher == nil {
		return nil
	}
	return s.deps.Publisher.Publish(ctx, models.DownloadEvent{Type: typ, Download: d, Timestamp: time.Now().UTC(), Data: data})
}

func mapStatusToEvent(st models.DownloadStatus) models.DownloadEventType {
	switch st {
	case models.StatusQueued:
		return models.EventEnqueued
	case models.StatusRunning:
		return models.EventStarted
	case models.StatusPaused:
		return models.EventPaused
	case models.StatusCompleted:
		return models.EventCompleted
	case models.StatusFailed:
		return models.EventFailed
	case models.StatusCancelled:
		return models.EventCancelled
	default:
		return models.EventProgress
	}
}
