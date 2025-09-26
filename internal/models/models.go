package models

import (
	"context"
	"time"
)

// ---- Proxy / Network ----

type Proxy struct {
	IP        string        `json:"ip"`
	Port      int           `json:"port"`
	Type      string        `json:"type"`
	Username  string        `json:"username,omitempty"`
	Password  string        `json:"password,omitempty"`
	TLSVerify bool          `json:"tls_verify,omitempty"`
	Timeout   time.Duration `json:"timeout,omitempty"`
	Failover  []string      `json:"failover,omitempty"`
	MaxConns  int           `json:"max_conns,omitempty"`
	AuthType  string        `json:"auth_type,omitempty"`
}

type Auth struct {
	Type           string `json:"type,omitempty"`
	Username       string `json:"username,omitempty"`
	Password       string `json:"password,omitempty"`
	BearerToken    string `json:"bearer_token,omitempty"`
	ClientCertPath string `json:"client_cert_path,omitempty"`
	ClientKeyPath  string `json:"client_key_path,omitempty"`
}

type TLSOptions struct {
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitempty"`
	CACertPath         string `json:"ca_cert_path,omitempty"`
	ServerName         string `json:"server_name,omitempty"`
	MinVersion         uint16 `json:"min_version,omitempty"`
	MaxVersion         uint16 `json:"max_version,omitempty"`
}

type DownloadConfig struct {
	MaxConnections   int           `json:"max_connections,omitempty"`
	BufferSize       int64         `json:"buffer_size,omitempty"`
	Proxy            *Proxy        `json:"proxy,omitempty"`
	Timeout          time.Duration `json:"timeout,omitempty"` // default is no timeout
	FollowRedirects  bool          `json:"follow_redirects,omitempty"`
	RedirectsLimit   int           `json:"redirects_limit,omitempty"`
	AllowInsecureTLS bool          `json:"allow_insecure_tls,omitempty"`
	TLS              *TLSOptions   `json:"tls,omitempty"`
	Auth             *Auth         `json:"auth,omitempty"`

	AcceptRanges bool `json:"accept_ranges,omitempty"`
	Resume       bool `json:"resume,omitempty"`

	Retries       int           `json:"retries,omitempty"`
	RetryDelay    time.Duration `json:"retry_delay,omitempty"`
	RetryJitter   time.Duration `json:"retry_jitter,omitempty"`
	BackoffFactor float64       `json:"backoff_factor,omitempty"`
	Retry         RetryPolicy   `json:"retry,omitempty"`

	RateLimit       int64 `json:"rate_limit,omitempty"`
	GlobalRateLimit bool  `json:"global_rate_limit,omitempty"`

	OnlyOnUnmetered   bool   `json:"only_on_unmetered,omitempty"`
	VerifyContentType bool   `json:"verify_content_type,omitempty"`
	Mime              string `json:"mime,omitempty"`

	Headers map[string]string `json:"headers,omitempty"`
	Cookies map[string]string `json:"cookies,omitempty"`

	DisableHead bool `json:"disable_head,omitempty"`
}

type FileOptions struct {
	Path           string `json:"path"`
	Filename       string `json:"filename,omitempty"`
	TempDir        string `json:"temp_dir,omitempty"`
	Overwrite      bool   `json:"overwrite,omitempty"`
	UniqueFilename bool   `json:"unique_filename,omitempty"`

	Checksum     string `json:"checksum,omitempty"`
	ChecksumType string `json:"checksum_type,omitempty"`
	MaxFileSize  int64  `json:"max_file_size,omitempty"`
}

type RequestOptions struct {
	URL        string   `json:"url"`
	UserAgent  string   `json:"user_agent,omitempty"`
	MirrorURLs []string `json:"mirror_urls,omitempty"`

	Extra *struct {
		Method           string            `json:"method,omitempty"`
		QueryParams      map[string]string `json:"query_params,omitempty"`
		Headers          map[string]string `json:"headers,omitempty"`
		Cookies          map[string]string `json:"cookies,omitempty"`
		Body             string            `json:"body,omitempty"`
		ContentLength    int64             `json:"content_length,omitempty"`
		ContentType      string            `json:"content_type,omitempty"`
		Protocol         string            `json:"protocol,omitempty"`
		FollowRedirects  bool              `json:"follow_redirects,omitempty"`
		AllowInsecureTLS bool              `json:"allow_insecure_tls,omitempty"`
	} `json:"extra,omitempty"`
}

// ResponseMetadata captures server response attributes useful for resuming and validation
type ResponseMetadata struct {
	ETag          string `json:"etag,omitempty"`
	LastModified  string `json:"last_modified,omitempty"`
	AcceptRanges  bool   `json:"accept_ranges,omitempty"`
	ContentType   string `json:"content_type,omitempty"`
	ContentLength int64  `json:"content_length,omitempty"`
}

const DefaultQueueName = "main"

type DownloadStatus string

const (
	StatusPending   DownloadStatus = "pending"
	StatusQueued    DownloadStatus = "queued"
	StatusRunning   DownloadStatus = "running"
	StatusPaused    DownloadStatus = "paused"
	StatusCompleted DownloadStatus = "completed"
	StatusFailed    DownloadStatus = "failed"
	StatusCancelled DownloadStatus = "cancelled"
)

type SegmentStatus string

const (
	SegmentPending   SegmentStatus = "pending"
	SegmentRunning   SegmentStatus = "running"
	SegmentCompleted SegmentStatus = "completed"
	SegmentFailed    SegmentStatus = "failed"
)

type RetryPolicy struct {
	MaxRetries    int           `json:"max_retries,omitempty"`
	RetryDelay    time.Duration `json:"retry_delay,omitempty"`
	BackoffFactor float64       `json:"backoff_factor,omitempty"`
	Jitter        time.Duration `json:"jitter,omitempty"`
}

// Strongly typed queue identifier
type QueueName string

type Queue struct {
	ID          string         `json:"id"`
	Name        QueueName      `json:"name"`
	Concurrency int            `json:"concurrency,omitempty"`
	RateLimit   int64          `json:"rate_limit,omitempty"`
	Default     bool           `json:"default,omitempty"`
	Paused      bool           `json:"paused,omitempty"`
	RetryPolicy RetryPolicy    `json:"retry_policy,omitempty"`
	Config      DownloadConfig `json:"config"`
	CreatedAt   time.Time      `json:"created_at,omitempty"`
	UpdatedAt   time.Time      `json:"updated_at,omitempty"`
}

type QueueStats struct {
	QueueID      string `json:"queue_id"`
	NumPending   int    `json:"num_pending"`
	NumRunning   int    `json:"num_running"`
	NumCompleted int    `json:"num_completed"`
	NumFailed    int    `json:"num_failed"`
	BytesPerSec  int64  `json:"bytes_per_sec,omitempty"`
}

type Segment struct {
	ID             string        `json:"id"`
	DownloadID     string        `json:"download_id"`
	Index          int           `json:"index"`
	Start          int64         `json:"start"`
	End            int64         `json:"end"`
	BytesCompleted int64         `json:"bytes_completed"`
	Status         SegmentStatus `json:"status"`
	Retries        int           `json:"retries,omitempty"`
	Checksum       string        `json:"checksum,omitempty"`
	TempPath       string        `json:"temp_path,omitempty"`
	CreatedAt      time.Time     `json:"created_at,omitempty"`
	UpdatedAt      time.Time     `json:"updated_at,omitempty"`
}

type Download struct {
	ID       string   `json:"id"`
	URL      string   `json:"url"`
	QueueID  string   `json:"queue_id,omitempty"`
	Priority int      `json:"priority,omitempty"`
	Tags     []string `json:"tags,omitempty"`

	Request *RequestOptions `json:"request,omitempty"`
	Config  *DownloadConfig `json:"config,omitempty"`
	File    *FileOptions    `json:"file,omitempty"`

	Status           DownloadStatus `json:"status"`
	Error            string         `json:"error,omitempty"`
	RetriesAttempted int            `json:"retries_attempted,omitempty"`

	BytesTotal     int64   `json:"bytes_total,omitempty"`
	BytesCompleted int64   `json:"bytes_completed,omitempty"`
	Progress       float64 `json:"progress,omitempty"`
	AvgSpeed       int64   `json:"avg_speed,omitempty"`
	CurrentSpeed   int64   `json:"current_speed,omitempty"`

	Segments []Segment         `json:"segments,omitempty"`
	Response *ResponseMetadata `json:"response,omitempty"`

	CreatedAt   time.Time  `json:"created_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at,omitempty"`
	RequestedAt *time.Time `json:"requested_at,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`

	ctx    context.Context    `json:"-"`
	cancel context.CancelFunc `json:"-"`
}

type EnqueueDownloadRequest struct {
	URL      string          `json:"url"`
	Request  *RequestOptions `json:"request,omitempty"`
	Config   *DownloadConfig `json:"config,omitempty"`
	File     *FileOptions    `json:"file,omitempty"`
	QueueID  string          `json:"queue_id,omitempty"`
	Priority int             `json:"priority,omitempty"`
	Tags     []string        `json:"tags,omitempty"`
}

type EnqueueDownloadResponse struct {
	ID      string         `json:"id"`
	QueueID string         `json:"queue_id"`
	Status  DownloadStatus `json:"status"`
}
