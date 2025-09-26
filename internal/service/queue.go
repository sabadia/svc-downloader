package service

import (
	"context"
	"errors"
	"time"

	"github.com/sabadia/svc-downloader/internal/models"
)

type QueueServiceImpl struct {
	Repo models.Repository
}

func NewQueueService(repo models.Repository) *QueueServiceImpl { return &QueueServiceImpl{Repo: repo} }

func (s *QueueServiceImpl) CreateQueue(ctx context.Context, queue models.Queue) error {
	if queue.ID == "" {
		return errors.New("queue id is required")
	}
	queue.CreatedAt = time.Now().UTC()
	queue.UpdatedAt = queue.CreatedAt
	return s.Repo.SaveQueue(ctx, &queue)
}

func (s *QueueServiceImpl) GetQueue(ctx context.Context, id string) (*models.Queue, error) {
	return s.Repo.GetQueue(ctx, id)
}

func (s *QueueServiceImpl) ListQueues(ctx context.Context) ([]models.Queue, error) {
	return s.Repo.ListQueues(ctx)
}

func (s *QueueServiceImpl) UpdateQueue(ctx context.Context, queue models.Queue) error {
	if queue.ID == "" {
		return errors.New("queue id is required")
	}
	queue.UpdatedAt = time.Now().UTC()
	return s.Repo.SaveQueue(ctx, &queue)
}

func (s *QueueServiceImpl) DeleteQueue(ctx context.Context, id string) error {
	return s.Repo.DeleteQueue(ctx, id)
}

func (s *QueueServiceImpl) PauseQueue(ctx context.Context, id string) error {
	return s.Repo.RunInTx(ctx, func(ctx context.Context, tx models.Repository) error {
		q, err := tx.GetQueue(ctx, id)
		if err != nil {
			return err
		}
		q.Paused = true
		q.UpdatedAt = time.Now().UTC()
		return tx.SaveQueue(ctx, q)
	})
}

func (s *QueueServiceImpl) ResumeQueue(ctx context.Context, id string) error {
	return s.Repo.RunInTx(ctx, func(ctx context.Context, tx models.Repository) error {
		q, err := tx.GetQueue(ctx, id)
		if err != nil {
			return err
		}
		q.Paused = false
		q.UpdatedAt = time.Now().UTC()
		return tx.SaveQueue(ctx, q)
	})
}

func (s *QueueServiceImpl) Stats(ctx context.Context, id string) (models.QueueStats, error) {
	return s.Repo.GetQueueStats(ctx, id)
}

// Bulk helpers proxy to repository
func (s *QueueServiceImpl) BulkDeleteDownloads(ctx context.Context, id string) error {
	return s.Repo.BulkDeleteDownloadsByQueueID(ctx, id)
}
func (s *QueueServiceImpl) BulkReassign(ctx context.Context, fromID, toID string) error {
	return s.Repo.BulkReassignDownloadsQueue(ctx, fromID, toID)
}
func (s *QueueServiceImpl) BulkSetPriority(ctx context.Context, id string, priority int) error {
	return s.Repo.BulkSetPriorityByQueueID(ctx, id, priority)
}
func (s *QueueServiceImpl) BulkUpdateStatus(ctx context.Context, id string, status models.DownloadStatus) error {
	return s.Repo.BulkUpdateStatusByQueueID(ctx, id, status)
}
