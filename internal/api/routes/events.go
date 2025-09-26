package routes

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/sabadia/svc-downloader/internal/models"
)

type EventsAPI struct{ pub models.EventPublisher }

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
					_, _ = fmt.Fprintf(pw, "event: %s\n", ev.Type)
					_, _ = fmt.Fprintf(pw, "data: {\"id\":\"%s\",\"status\":\"%s\"}\n\n", ev.Download.ID, ev.Download.Status)
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
