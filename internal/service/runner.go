package service

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sabadia/svc-downloader/internal/models"
)

type DownloadRunner struct{ deps DownloadDeps }

func NewDownloadRunner(deps DownloadDeps) *DownloadRunner { return &DownloadRunner{deps: deps} }

func (r *DownloadRunner) Run(ctx context.Context, id string) {
	// Best-effort; internal goroutine, log/emit events via publisher in case of errors.
	d, err := r.deps.Repo.GetDownload(ctx, id)
	if err != nil {
		return
	}

	// Prepare download metadata and segments
	segs, err := r.prepareDownload(ctx, d)
	if err != nil {
		r.fail(ctx, d, err)
		return
	}

	// Download segments concurrently
	if err := r.downloadSegments(ctx, d, segs); err != nil {
		r.fail(ctx, d, err)
		return
	}

	// Finalize download (merge, verify, complete)
	r.finalizeDownload(ctx, d)
}

// prepareDownload fetches metadata and plans segments
func (r *DownloadRunner) prepareDownload(ctx context.Context, d *models.Download) ([]models.Segment, error) {
	// Prepare file system
	if err := r.deps.FileStore.Prepare(ctx, d); err != nil {
		return nil, err
	}

	// HEAD (with fallback handled in transport)
	md, _, err := r.deps.Transport.Head(ctx, *d.Request, pickCfg(d))
	if err != nil {
		return nil, err
	}

	// Verify content type if required
	if d.Config != nil && d.Config.VerifyContentType && d.Config.Mime != "" {
		if md.ContentType != "" && md.ContentType != d.Config.Mime {
			return nil, fmt.Errorf("unexpected content-type: %s", md.ContentType)
		}
	}

	d.Response = md
	if d.BytesTotal == 0 && md.ContentLength > 0 {
		d.BytesTotal = md.ContentLength
	}
	if err := r.deps.Repo.UpdateDownload(ctx, d); err != nil {
		return nil, err
	}

	// Plan segments (single-stream fallback if no Accept-Ranges)
	acceptRanges := md.AcceptRanges
	if d.Config != nil && !d.Config.AcceptRanges {
		acceptRanges = false
	}
	segs, err := r.deps.Planner.Plan(ctx, d, md.ContentLength, acceptRanges)
	if err != nil {
		return nil, err
	}

	return segs, nil
}

// downloadSegments manages the concurrent download of segments
func (r *DownloadRunner) downloadSegments(ctx context.Context, d *models.Download, segs []models.Segment) error {
	var wg sync.WaitGroup
	errCh := make(chan error, len(segs))
	progressCh := make(chan int64, len(segs))

	// Start progress monitoring goroutine
	go r.monitorProgress(ctx, d, progressCh)

	for i := range segs {
		seg := segs[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.downloadSegment(ctx, d, &seg, progressCh); err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)
	close(progressCh)

	for e := range errCh {
		if e != nil {
			return e
		}
	}
	return nil
}

// downloadSegment downloads a single segment
func (r *DownloadRunner) downloadSegment(ctx context.Context, d *models.Download, seg *models.Segment, progressCh chan<- int64) error {
	start := seg.Start
	// Use FileStore to determine existing bytes for resume
	probeW, existing, err := r.deps.FileStore.OpenSegmentWriter(ctx, d, *seg)
	if err != nil {
		return err
	}
	_ = probeW.Close()
	if existing > 0 {
		start += existing
	}

	attempts := 0
	maxRetries := 3
	baseDelay := time.Second
	if d.Config != nil {
		if d.Config.Retry.MaxRetries > 0 {
			maxRetries = d.Config.Retry.MaxRetries
		}
		if d.Config.Retry.RetryDelay > 0 {
			baseDelay = d.Config.Retry.RetryDelay
		}
	}
	var end = seg.End

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// rate limit per global/queue/download
		if r.deps.RateLimiter != nil {
			if wait, _ := r.deps.RateLimiter.Reserve(ctx, "global", safeSpan(end, start)); wait > 0 {
				time.Sleep(wait)
			}
			if d.QueueID != "" {
				if wait, _ := r.deps.RateLimiter.Reserve(ctx, d.QueueID, safeSpan(end, start)); wait > 0 {
					time.Sleep(wait)
				}
			}
			if wait, _ := r.deps.RateLimiter.Reserve(ctx, d.ID, safeSpan(end, start)); wait > 0 {
				time.Sleep(wait)
			}
		}

		body, _, hdrs, code, err := r.deps.Transport.GetRange(ctx, *d.Request, pickCfg(d), start, end)
		if err != nil || (code != 200 && code != 206) {
			attempts++
			d.RetriesAttempted = attempts
			_ = r.deps.Repo.UpdateDownload(ctx, d)
			if attempts > maxRetries {
				return err
			}
			// backoff with jitter
			factor := 1.5
			if d.Config != nil && d.Config.BackoffFactor > 0 {
				factor = d.Config.BackoffFactor
			}
			delay := time.Duration(float64(baseDelay) * pow(factor, float64(attempts-1)))
			if d.Config != nil && d.Config.Retry.Jitter > 0 {
				j := rand.Int63n(int64(d.Config.Retry.Jitter))
				delay += time.Duration(j)
			}
			time.Sleep(delay)
			continue
		}

		// Validate Content-Range for 206
		if code == 206 {
			cr := ""
			if v, ok := hdrs["Content-Range"]; ok && len(v) > 0 {
				cr = v[0]
			}
			if cr != "" {
				// format: bytes start-end/total
				parts := strings.Fields(cr)
				if len(parts) == 2 && strings.ToLower(parts[0]) == "bytes" {
					rangePart := parts[1]
					if dash := strings.Index(rangePart, "/"); dash > 0 {
						rangeOnly := rangePart[:dash]
						if hy := strings.Index(rangeOnly, "-"); hy > 0 {
							rs := rangeOnly[:hy]
							re := rangeOnly[hy+1:]
							if rsn, err1 := strconv.ParseInt(rs, 10, 64); err1 == nil {
								if rsn != start {
									// unexpected start, retry
									body.Close()
									attempts++
									time.Sleep(time.Second)
									continue
								}
							}
							if end >= 0 {
								if ren, err2 := strconv.ParseInt(re, 10, 64); err2 == nil {
									if ren < start || ren > end {
										body.Close()
										attempts++
										time.Sleep(time.Second)
										continue
									}
								}
							}
						}
					}
				}
			}
		}

		w, _, err := r.deps.FileStore.OpenSegmentWriter(ctx, d, *seg)
		if err != nil {
			body.Close()
			return err
		}

		// Prepare reader and enforce boundaries if server ignored Range (200)
		var reader io.Reader = body
		if code == 200 {
			if start > 0 {
				if _, err := io.CopyN(io.Discard, body, start); err != nil {
					_ = w.Close()
					body.Close()
					attempts++
					if attempts > maxRetries {
						return err
					}
					time.Sleep(time.Duration(attempts) * time.Second)
					continue
				}
			}
			if end >= 0 {
				span := end - start + 1
				if span < 0 {
					span = 0
				}
				reader = io.LimitReader(body, span)
			}
		}

		// Max file size enforcement
		if d.File != nil && d.File.MaxFileSize > 0 {
			remaining := d.File.MaxFileSize - atomic.LoadInt64(&d.BytesCompleted)
			if remaining < 0 {
				remaining = 0
			}
			reader = io.LimitReader(reader, remaining)
		}

		// Per-segment hash while writing (configurable)
		var h hash.Hash
		sht := "md5"
		if d.Config != nil && d.Config.SegmentChecksumType != "" {
			sht = strings.ToLower(d.Config.SegmentChecksumType)
		}
		switch sht {
		case "md5":
			h = md5.New()
		case "sha1":
			h = sha1.New()
		case "sha256":
			h = sha256.New()
		case "crc32c":
			h = crc32.New(crc32.MakeTable(crc32.Castagnoli))
		case "none":
			h = nil
		default:
			h = md5.New()
		}
		if h != nil {
			reader = io.TeeReader(reader, h)
		}

		var written int64
		if d.Config != nil && d.Config.BufferSize > 0 {
			buf := make([]byte, d.Config.BufferSize)
			written, err = io.CopyBuffer(w, reader, buf)
		} else {
			written, err = io.Copy(w, reader)
		}
		body.Close()
		_ = w.Close()

		if err != nil && err != io.EOF {
			attempts++
			if attempts > maxRetries {
				return err
			}
			time.Sleep(time.Duration(attempts) * time.Second)
			continue
		}

		// Validate bytes written vs expected span when known
		if end >= 0 {
			expected := (end - seg.Start + 1)
			totalNow := existing + written
			if totalNow != expected {
				attempts++
				if attempts > maxRetries {
					return io.ErrUnexpectedEOF
				}
				time.Sleep(time.Second)
				continue
			}
		}

		// Send progress update atomically
		progressCh <- written

		// Record segment checksum
		if h != nil {
			seg.Checksum = hex.EncodeToString(h.Sum(nil))
		}
		seg.BytesCompleted = existing + written
		break
	}

	seg.Status = models.SegmentCompleted
	seg.UpdatedAt = time.Now().UTC()
	_ = r.deps.Repo.UpsertSegment(ctx, seg)
	_ = r.deps.FileStore.CompleteSegment(ctx, d, *seg)
	return nil
}

// monitorProgress handles progress updates atomically
func (r *DownloadRunner) monitorProgress(ctx context.Context, d *models.Download, progressCh <-chan int64) {
	for written := range progressCh {
		// Atomically update progress
		atomic.AddInt64(&d.BytesCompleted, written)

		// Update total if needed (only once)
		if d.BytesTotal == 0 {
			// This is a best-effort update, the actual total will be set during preparation
		}

		// Calculate progress percentage
		if d.BytesTotal > 0 {
			completed := atomic.LoadInt64(&d.BytesCompleted)
			d.Progress = float64(completed) / float64(d.BytesTotal)
		}

		d.UpdatedAt = time.Now().UTC()
		_ = r.deps.Repo.UpdateDownload(ctx, d)
		_ = r.deps.Publisher.Publish(ctx, models.DownloadEvent{
			Type:      models.EventProgress,
			Download:  *d,
			Timestamp: time.Now().UTC(),
			Data:      map[string]any{"bytes": written},
		})
	}
}

// finalizeDownload merges segments, verifies checksums, and marks the download as complete
func (r *DownloadRunner) finalizeDownload(ctx context.Context, d *models.Download) {
	// Mark merge state
	d.Error = ""
	d.UpdatedAt = time.Now().UTC()
	_ = r.deps.Repo.UpdateDownload(ctx, d)

	// Merge and verify
	if err := r.deps.FileStore.MergeSegments(ctx, d); err != nil {
		r.fail(ctx, d, err)
		return
	}

	ok, err := r.deps.FileStore.VerifyChecksum(ctx, d)
	if err != nil || !ok {
		if err == nil {
			r.fail(ctx, d, io.ErrUnexpectedEOF)
		} else {
			r.fail(ctx, d, err)
		}
		return
	}

	// Complete
	d.Status = models.StatusCompleted
	now := time.Now().UTC()
	d.CompletedAt = &now
	d.UpdatedAt = now
	_ = r.deps.Repo.UpdateDownload(ctx, d)
	_ = r.deps.Publisher.Publish(ctx, models.DownloadEvent{Type: models.EventCompleted, Download: *d, Timestamp: time.Now().UTC()})
}

func (r *DownloadRunner) fail(ctx context.Context, d *models.Download, err error) {
	d.Status = models.StatusFailed
	d.Error = err.Error()
	d.UpdatedAt = time.Now().UTC()
	_ = r.deps.Repo.UpdateDownload(ctx, d)
	_ = r.deps.Publisher.Publish(ctx, models.DownloadEvent{Type: models.EventFailed, Download: *d, Timestamp: time.Now().UTC(), Data: map[string]any{"error": err.Error()}})
}

func pickCfg(d *models.Download) models.DownloadConfig {
	if d.Config != nil {
		return *d.Config
	}
	return models.DownloadConfig{}
}

func pow(a, b float64) float64 { return mathPow(a, b) }

// inline small power function to avoid pulling math.Pow in hot path
func mathPow(a, b float64) float64 {
	return float64(int64((1+b*0)+0)) * // dummy to keep compiler happy inlining; replace with math.Pow if desired
		func() float64 { // fallback to math.Pow
			return math.Pow(a, b)
		}()
}

func safeSpan(end int64, start int64) int64 {
	if end < 0 {
		// unknown end, reserve a reasonable chunk (e.g., 1MB)
		return 1 << 20
	}
	sz := end - start + 1
	if sz < 0 {
		return 0
	}
	return sz
}
