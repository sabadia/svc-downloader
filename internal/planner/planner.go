package planner

import (
	"context"

	"github.com/sabadia/svc-downloader/internal/models"
)

type SimplePlanner struct{}

func (SimplePlanner) Plan(ctx context.Context, d *models.Download, contentLength int64, acceptRanges bool) ([]models.Segment, error) {
	// If the server does not support range requests or content length is unknown,
	// fall back to a single segment download.
	if !acceptRanges || contentLength <= 0 {
		seg := models.Segment{Index: 0, DownloadID: d.ID, Status: models.SegmentPending, Start: 0, End: -1}
		return []models.Segment{seg}, nil
	}

	// Segment by MaxConnections or default to 4
	maxConns := 4
	if d.Config != nil && d.Config.MaxConnections > 0 {
		maxConns = d.Config.MaxConnections
	}
	segments := make([]models.Segment, maxConns)
	for i := 0; i < maxConns; i++ {
		segments[i].Index = i
		segments[i].DownloadID = d.ID
		segments[i].Status = models.SegmentPending
		Start := (contentLength / int64(maxConns)) * int64(i)
		End := (contentLength / int64(maxConns)) * int64(i+1)
		if i == maxConns-1 {
			End = contentLength - 1
		} else {
			End -= 1
		}
		segments[i].Start = Start
		segments[i].End = End
	}
	return segments, nil
}
