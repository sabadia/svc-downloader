package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/sabadia/svc-downloader/internal/models"
)

type EventsAPI struct{ pub models.EventPublisher }

// SSEEvent represents a Server-Sent Event
type SSEEvent struct {
	ID     string                 `json:"id"`
	Status string                 `json:"status"`
	Data   map[string]interface{} `json:"data,omitempty"`
}

func RegisterEvents(api huma.API, pub models.EventPublisher) {
	h := &EventsAPI{pub: pub}

	huma.Register(api, huma.Operation{
		OperationID: "events",
		Method:      http.MethodGet,
		Path:        "/events",
		Summary:     "Server Sent Events",
	}, func(ctx context.Context, input *struct{}) (*struct {
		Body io.ReadCloser `contentType:"text/event-stream"`
	}, error) {
		ch, cancel, err := h.pub.Subscribe(ctx,
			models.EventEnqueued, models.EventStarted, models.EventProgress, models.EventPaused, models.EventResumed, models.EventCompleted, models.EventFailed, models.EventCancelled,
		)
		if err != nil {
			return nil, err
		}
		pr, pw := io.Pipe()
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			defer func() {
				cancel()
				_ = pw.Close()
			}()
			for {
				select {
				case <-ctx.Done():
					return
				case ev := <-ch:
					sseEvent := SSEEvent{
						ID:     ev.Download.ID,
						Status: string(ev.Download.Status),
						Data:   ev.Data,
					}

					eventData, err := json.Marshal(sseEvent)
					if err != nil {
						// Log error but continue
						continue
					}

					_, _ = fmt.Fprintf(pw, "event: %s\n", ev.Type)
					_, _ = fmt.Fprintf(pw, "data: %s\n\n", string(eventData))
				case <-ticker.C:
					_, _ = io.WriteString(pw, ": keep-alive\n\n")
				}
			}
		}()
		return &struct {
			Body io.ReadCloser `contentType:"text/event-stream"`
		}{Body: pr}, nil
	})
}
