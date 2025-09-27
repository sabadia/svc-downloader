package routes

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/sabadia/svc-downloader/internal/models"
)

type QueuesAPI struct{ Svc models.QueueService }

func RegisterQueues(api huma.API, svc models.QueueService) {
	h := &QueuesAPI{Svc: svc}

	huma.Register(api, huma.Operation{OperationID: "createQueue", Method: http.MethodPost, Path: "/queues"}, func(ctx context.Context, input *struct{ Body models.Queue }) (*struct{}, error) {
		return &struct{}{}, h.Svc.CreateQueue(ctx, input.Body)
	})

	huma.Register(api, huma.Operation{OperationID: "getQueue", Method: http.MethodGet, Path: "/queues/{id}"}, func(ctx context.Context, input *struct {
		ID string `path:"id"`
	}) (*struct{ Body *models.Queue }, error) {
		q, err := h.Svc.GetQueue(ctx, input.ID)
		if err != nil {
			return nil, err
		}
		return &struct{ Body *models.Queue }{Body: q}, nil
	})

	huma.Register(api, huma.Operation{OperationID: "listQueues", Method: http.MethodGet, Path: "/queues"}, func(ctx context.Context, input *struct{}) (*struct{ Body []models.Queue }, error) {
		qs, err := h.Svc.ListQueues(ctx)
		if err != nil {
			return nil, err
		}
		return &struct{ Body []models.Queue }{Body: qs}, nil
	})

	huma.Register(api, huma.Operation{OperationID: "updateQueue", Method: http.MethodPut, Path: "/queues/{id}"}, func(ctx context.Context, input *struct {
		ID   string `path:"id"`
		Body models.Queue
	}) (*struct{}, error) {
		input.Body.ID = input.ID
		return &struct{}{}, h.Svc.UpdateQueue(ctx, input.Body)
	})

	huma.Register(api, huma.Operation{OperationID: "deleteQueue", Method: http.MethodDelete, Path: "/queues/{id}"}, func(ctx context.Context, input *struct {
		ID string `path:"id"`
	}) (*struct{}, error) {
		return &struct{}{}, h.Svc.DeleteQueue(ctx, input.ID)
	})

	huma.Register(api, huma.Operation{OperationID: "pauseQueue", Method: http.MethodPost, Path: "/queues/{id}/pause"}, func(ctx context.Context, input *struct {
		ID string `path:"id"`
	}) (*struct{}, error) {
		return &struct{}{}, h.Svc.PauseQueue(ctx, input.ID)
	})

	huma.Register(api, huma.Operation{OperationID: "resumeQueue", Method: http.MethodPost, Path: "/queues/{id}/resume"}, func(ctx context.Context, input *struct {
		ID string `path:"id"`
	}) (*struct{}, error) {
		return &struct{}{}, h.Svc.ResumeQueue(ctx, input.ID)
	})

	huma.Register(api, huma.Operation{OperationID: "queueStats", Method: http.MethodGet, Path: "/queues/{id}/stats"}, func(ctx context.Context, input *struct {
		ID string `path:"id"`
	}) (*struct{ Body models.QueueStats }, error) {
		s, err := h.Svc.Stats(ctx, input.ID)
		if err != nil {
			return nil, err
		}
		return &struct{ Body models.QueueStats }{Body: s}, nil
	})

	// Bulk operations
	huma.Register(api, huma.Operation{OperationID: "queueBulkDelete", Method: http.MethodPost, Path: "/queues/{id}/bulk/delete"}, func(ctx context.Context, input *struct {
		ID string `path:"id"`
	}) (*struct{}, error) {
		return &struct{}{}, h.Svc.BulkDeleteDownloads(ctx, input.ID)
	})

	huma.Register(api, huma.Operation{OperationID: "queueBulkReassign", Method: http.MethodPost, Path: "/queues/{id}/bulk/reassign"}, func(ctx context.Context, input *struct {
		ID   string `path:"id"`
		Body *struct {
			To string `json:"to"`
		}
	}) (*struct{}, error) {
		return &struct{}{}, h.Svc.BulkReassign(ctx, input.ID, input.Body.To)
	})

	huma.Register(api, huma.Operation{OperationID: "queueBulkPriority", Method: http.MethodPost, Path: "/queues/{id}/bulk/priority"}, func(ctx context.Context, input *struct {
		ID   string `path:"id"`
		Body *struct {
			Priority int `json:"priority"`
		}
	}) (*struct{}, error) {
		return &struct{}{}, h.Svc.BulkSetPriority(ctx, input.ID, input.Body.Priority)
	})

	huma.Register(api, huma.Operation{OperationID: "queueBulkStatus", Method: http.MethodPost, Path: "/queues/{id}/bulk/status"}, func(ctx context.Context, input *struct {
		ID   string `path:"id"`
		Body *struct {
			Status models.DownloadStatus `json:"status"`
		}
	}) (*struct{}, error) {
		return &struct{}{}, h.Svc.BulkUpdateStatus(ctx, input.ID, input.Body.Status)
	})
}
