package models

import (
	"context"
	"io"
	"time"
)

type ListDownloadsOptions struct {
	Statuses      []DownloadStatus
	QueueIDs      []string
	Tags          []string
	Search        string
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
	OrderBy       string
	OrderDesc     bool
}

type DownloadService interface {
	Enqueue(ctx context.Context, req EnqueueDownloadRequest) (EnqueueDownloadResponse, error)
	Start(ctx context.Context, id string) error
	Pause(ctx context.Context, id string) error
	Resume(ctx context.Context, id string) error
	Cancel(ctx context.Context, id string) error
	Remove(ctx context.Context, id string, deleteFiles bool) error

	Get(ctx context.Context, id string) (*Download, error)
	List(ctx context.Context, options ListDownloadsOptions, limit, offset int) ([]Download, error)
	Count(ctx context.Context, options ListDownloadsOptions) (int, error)

	UpdateConfig(ctx context.Context, id string, config DownloadConfig) error
	UpdateRequest(ctx context.Context, id string, request RequestOptions) error
	SetPriority(ctx context.Context, id string, priority int) error
	AddTags(ctx context.Context, id string, tags ...string) error
	RemoveTags(ctx context.Context, id string, tags ...string) error

	AssignQueue(ctx context.Context, id string, queueID string) error
}

type QueueService interface {
	CreateQueue(ctx context.Context, queue Queue) error
	GetQueue(ctx context.Context, id string) (*Queue, error)
	ListQueues(ctx context.Context) ([]Queue, error)
	UpdateQueue(ctx context.Context, queue Queue) error
	DeleteQueue(ctx context.Context, id string) error

	PauseQueue(ctx context.Context, id string) error
	ResumeQueue(ctx context.Context, id string) error
	Stats(ctx context.Context, id string) (QueueStats, error)

	BulkDeleteDownloads(ctx context.Context, id string) error
	BulkReassign(ctx context.Context, fromID, toID string) error
	BulkSetPriority(ctx context.Context, id string, priority int) error
	BulkUpdateStatus(ctx context.Context, id string, status DownloadStatus) error
}

type SegmentPlanner interface {
	Plan(ctx context.Context, d *Download, contentLength int64, acceptRanges bool) ([]Segment, error)
}

type TransportClient interface {
	Head(ctx context.Context, req RequestOptions, cfg DownloadConfig) (*ResponseMetadata, map[string][]string, error)
	GetRange(ctx context.Context, req RequestOptions, cfg DownloadConfig, startInclusive int64, endInclusive int64) (io.ReadCloser, *ResponseMetadata, map[string][]string, int, error)
}

type FileStore interface {
	Prepare(ctx context.Context, d *Download) error
	OpenSegmentWriter(ctx context.Context, d *Download, s Segment) (io.WriteCloser, error)
	CompleteSegment(ctx context.Context, d *Download, s Segment) error
	MergeSegments(ctx context.Context, d *Download) error
	RemoveDownloadFiles(ctx context.Context, d *Download) error
	VerifyChecksum(ctx context.Context, d *Download) (bool, error)
}

type RateLimiter interface {
	Reserve(ctx context.Context, key string, bytes int64) (time.Duration, error)
	SetLimit(key string, bytesPerSecond int64)
}

type DownloadEventType string

const (
	EventEnqueued  DownloadEventType = "enqueued"
	EventStarted   DownloadEventType = "started"
	EventProgress  DownloadEventType = "progress"
	EventPaused    DownloadEventType = "paused"
	EventResumed   DownloadEventType = "resumed"
	EventCompleted DownloadEventType = "completed"
	EventFailed    DownloadEventType = "failed"
	EventCancelled DownloadEventType = "cancelled"
)

type DownloadEvent struct {
	Type      DownloadEventType
	Download  Download
	Timestamp time.Time
	Data      map[string]any
}

type EventPublisher interface {
	Publish(ctx context.Context, event DownloadEvent) error
	Subscribe(ctx context.Context, types ...DownloadEventType) (<-chan DownloadEvent, func(), error)
}

type Validator interface {
	ValidateRequest(req RequestOptions) error
	ValidateConfig(cfg DownloadConfig) error
	ValidateFileOptions(file FileOptions) error
}

type Repository interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context, tx Repository) error) error

	CreateDownload(ctx context.Context, d *Download) error
	UpdateDownload(ctx context.Context, d *Download) error
	GetDownload(ctx context.Context, id string) (*Download, error)
	ListDownloads(ctx context.Context, options ListDownloadsOptions, limit, offset int) ([]Download, error)
	CountDownloads(ctx context.Context, options ListDownloadsOptions) (int, error)
	DeleteDownload(ctx context.Context, id string) error

	UpsertSegment(ctx context.Context, s *Segment) error
	ListSegments(ctx context.Context, downloadID string) ([]Segment, error)
	UpdateSegment(ctx context.Context, s *Segment) error
	DeleteSegments(ctx context.Context, downloadID string) error

	SaveQueue(ctx context.Context, q *Queue) error
	GetQueue(ctx context.Context, id string) (*Queue, error)
	ListQueues(ctx context.Context) ([]Queue, error)
	DeleteQueue(ctx context.Context, id string) error

	SaveQueueStats(ctx context.Context, stats QueueStats) error
	GetQueueStats(ctx context.Context, id string) (QueueStats, error)

	// Bulk operations
	BulkCreateDownloads(ctx context.Context, downloads []Download) error
	BulkUpdateDownloads(ctx context.Context, downloads []Download) error
	BulkDeleteDownloads(ctx context.Context, ids []string) error

	// Queue-scoped bulk operations
	BulkDeleteDownloadsByQueueID(ctx context.Context, queueID string) error
	BulkReassignDownloadsQueue(ctx context.Context, fromQueueID, toQueueID string) error
	BulkSetPriorityByQueueID(ctx context.Context, queueID string, priority int) error
	BulkUpdateStatusByQueueID(ctx context.Context, queueID string, status DownloadStatus) error
}
