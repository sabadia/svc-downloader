package service

import (
	"context"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
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
	// Prepare file system
	if err := r.deps.FileStore.Prepare(ctx, d); err != nil {
		r.fail(ctx, d, err)
		return
	}
	// HEAD (with fallback handled in transport)
	md, _, err := r.deps.Transport.Head(ctx, *d.Request, pickCfg(d))
	if err != nil {
		r.fail(ctx, d, err)
		return
	}
	// Verify content type if required
	if d.Config != nil && d.Config.VerifyContentType && d.Config.Mime != "" {
		if md.ContentType != "" && md.ContentType != d.Config.Mime {
			r.fail(ctx, d, fmt.Errorf("unexpected content-type: %s", md.ContentType))
			return
		}
	}
	d.Response = md
	if d.BytesTotal == 0 && md.ContentLength > 0 {
		d.BytesTotal = md.ContentLength
	}
	if err := r.deps.Repo.UpdateDownload(ctx, d); err != nil {
		return
	}
	// Plan segments (single-stream fallback if no Accept-Ranges)
	acceptRanges := md.AcceptRanges
	if d.Config != nil && !d.Config.AcceptRanges {
		acceptRanges = false
	}
	segs, err := r.deps.Planner.Plan(ctx, d, md.ContentLength, acceptRanges)
	if err != nil {
		r.fail(ctx, d, err)
		return
	}
	var wg sync.WaitGroup
	errCh := make(chan error, len(segs))
	progMu := sync.Mutex{}
	for i := range segs {
		seg := segs[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := seg.Start
			// Determine part path and existing size
			tmpDir := filepath.Join(d.File.Path, ".tmp", d.ID)
			partPath := filepath.Join(tmpDir, fmt.Sprintf("%09d.part", seg.Index))
			if st, err := os.Stat(partPath); err == nil {
				if st.Size() > 0 {
					start += st.Size()
				}
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
					return
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
				body, smd, _, code, err := r.deps.Transport.GetRange(ctx, *d.Request, pickCfg(d), start, end)
				if err != nil || (code != 200 && code != 206) {
					attempts++
					d.RetriesAttempted = attempts
					_ = r.deps.Repo.UpdateDownload(ctx, d)
					if attempts > maxRetries {
						errCh <- err
						return
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
				w2, err := r.deps.FileStore.OpenSegmentWriter(ctx, d, seg)
				if err != nil {
					body.Close()
					errCh <- err
					return
				}
				// Max file size enforcement
				var reader io.Reader = body
				if d.File != nil && d.File.MaxFileSize > 0 {
					reader = io.LimitReader(body, d.File.MaxFileSize-(d.BytesCompleted))
				}
				var written int64
				if d.Config != nil && d.Config.BufferSize > 0 {
					buf := make([]byte, d.Config.BufferSize)
					written, err = io.CopyBuffer(w2, reader, buf)
				} else {
					written, err = io.Copy(w2, reader)
				}
				body.Close()
				_ = w2.Close()
				if err != nil && err != io.EOF {
					attempts++
					if attempts > maxRetries {
						errCh <- err
						return
					}
					time.Sleep(time.Duration(attempts) * time.Second)
					continue
				}
				// Update progress
				progMu.Lock()
				d.BytesCompleted += written
				if d.BytesTotal == 0 && smd != nil && smd.ContentLength > 0 {
					d.BytesTotal = smd.ContentLength
				}
				if d.BytesTotal > 0 {
					d.Progress = float64(d.BytesCompleted) / float64(d.BytesTotal)
				}
				d.UpdatedAt = time.Now().UTC()
				_ = r.deps.Repo.UpdateDownload(ctx, d)
				_ = r.deps.Publisher.Publish(ctx, models.DownloadEvent{Type: models.EventProgress, Download: *d, Timestamp: time.Now().UTC(), Data: map[string]any{"segment": seg.Index, "bytes": smd.ContentLength}})
				progMu.Unlock()
				break
			}
			seg.Status = models.SegmentCompleted
			seg.UpdatedAt = time.Now().UTC()
			_ = r.deps.Repo.UpsertSegment(ctx, &seg)
		}()
	}
	wg.Wait()
	close(errCh)
	for e := range errCh {
		if e != nil {
			r.fail(ctx, d, e)
			return
		}
	}
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
