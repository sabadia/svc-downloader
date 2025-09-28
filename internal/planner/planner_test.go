package planner

import (
	"context"
	"testing"

	"github.com/sabadia/svc-downloader/internal/models"
)

func TestSimplePlanner_Plan(t *testing.T) {
	planner := SimplePlanner{}
	ctx := context.Background()

	tests := []struct {
		name           string
		download       *models.Download
		contentLength  int64
		acceptRanges   bool
		expectedCount  int
		expectedRanges [][2]int64
	}{
		{
			name: "no range support - single segment",
			download: &models.Download{
				ID: "test-1",
			},
			contentLength:  1000,
			acceptRanges:   false,
			expectedCount:  1,
			expectedRanges: [][2]int64{{0, -1}},
		},
		{
			name: "unknown content length - single segment",
			download: &models.Download{
				ID: "test-2",
			},
			contentLength:  0,
			acceptRanges:   true,
			expectedCount:  1,
			expectedRanges: [][2]int64{{0, -1}},
		},
		{
			name: "range support with default connections",
			download: &models.Download{
				ID: "test-3",
			},
			contentLength: 1000,
			acceptRanges:  true,
			expectedCount: 4,
			expectedRanges: [][2]int64{
				{0, 249},   // 0-249
				{250, 499}, // 250-499
				{500, 749}, // 500-749
				{750, 999}, // 750-999
			},
		},
		{
			name: "range support with custom connections",
			download: &models.Download{
				ID: "test-4",
				Config: &models.DownloadConfig{
					MaxConnections: 3,
				},
			},
			contentLength: 900,
			acceptRanges:  true,
			expectedCount: 3,
			expectedRanges: [][2]int64{
				{0, 299},   // 0-299
				{300, 599}, // 300-599
				{600, 899}, // 600-899
			},
		},
		{
			name: "range support with single connection",
			download: &models.Download{
				ID: "test-5",
				Config: &models.DownloadConfig{
					MaxConnections: 1,
				},
			},
			contentLength: 1000,
			acceptRanges:  true,
			expectedCount: 1,
			expectedRanges: [][2]int64{
				{0, 999}, // 0-999
			},
		},
		{
			name: "range support with more connections than content length",
			download: &models.Download{
				ID: "test-6",
				Config: &models.DownloadConfig{
					MaxConnections: 10,
				},
			},
			contentLength: 5,
			acceptRanges:  true,
			expectedCount: 10,
			expectedRanges: [][2]int64{
				{0, -1}, {0, -1}, {0, -1}, {0, -1}, {0, -1},
				{0, -1}, {0, -1}, {0, -1}, {0, -1}, {0, 4}, // Last segment gets remainder
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			segments, err := planner.Plan(ctx, tt.download, tt.contentLength, tt.acceptRanges)

			if err != nil {
				t.Fatalf("Plan() error = %v", err)
			}

			if len(segments) != tt.expectedCount {
				t.Errorf("Plan() got %d segments, want %d", len(segments), tt.expectedCount)
			}

			for i, seg := range segments {
				if seg.DownloadID != tt.download.ID {
					t.Errorf("Segment[%d].DownloadID = %v, want %v", i, seg.DownloadID, tt.download.ID)
				}
				if seg.Index != i {
					t.Errorf("Segment[%d].Index = %v, want %v", i, seg.Index, i)
				}
				if seg.Status != models.SegmentPending {
					t.Errorf("Segment[%d].Status = %v, want %v", i, seg.Status, models.SegmentPending)
				}
				if i < len(tt.expectedRanges) {
					expected := tt.expectedRanges[i]
					if seg.Start != expected[0] {
						t.Errorf("Segment[%d].Start = %v, want %v", i, seg.Start, expected[0])
					}
					if seg.End != expected[1] {
						t.Errorf("Segment[%d].End = %v, want %v", i, seg.End, expected[1])
					}
				}
			}
		})
	}
}

func TestSimplePlanner_Plan_EdgeCases(t *testing.T) {
	planner := SimplePlanner{}
	ctx := context.Background()

	t.Run("negative content length", func(t *testing.T) {
		download := &models.Download{ID: "test-negative"}
		segments, err := planner.Plan(ctx, download, -100, true)
		if err != nil {
			t.Fatalf("Plan() error = %v", err)
		}
		if len(segments) != 1 {
			t.Errorf("Plan() got %d segments, want 1", len(segments))
		}
		if segments[0].End != -1 {
			t.Errorf("Segment.End = %v, want -1", segments[0].End)
		}
	})

	t.Run("very small content length", func(t *testing.T) {
		download := &models.Download{ID: "test-small"}
		segments, err := planner.Plan(ctx, download, 1, true)
		if err != nil {
			t.Fatalf("Plan() error = %v", err)
		}
		if len(segments) != 4 {
			t.Errorf("Plan() got %d segments, want 4", len(segments))
		}
		// All segments should be 0--1 except the last one which should be 0-0
		for i, seg := range segments {
			if i < len(segments)-1 {
				if seg.Start != 0 || seg.End != -1 {
					t.Errorf("Segment[%d] = %d-%d, want 0--1", i, seg.Start, seg.End)
				}
			} else {
				if seg.Start != 0 || seg.End != 0 {
					t.Errorf("Segment[%d] = %d-%d, want 0-0", i, seg.Start, seg.End)
				}
			}
		}
	})
}
