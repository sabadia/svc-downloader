package routes

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/sabadia/svc-downloader/internal/errs"
	"github.com/sabadia/svc-downloader/internal/models"
)

type API struct {
	Svc models.DownloadService
}

type EventService interface {
	Subscribe(ctx context.Context, types ...models.DownloadEventType) (<-chan models.DownloadEvent, func(), error)
}

func Register(api huma.API, svc models.DownloadService) {
	h := &API{Svc: svc}

	huma.Register(api, huma.Operation{
		OperationID: "enqueueDownload",
		Method:      http.MethodPost,
		Path:        "/downloads",
		Summary:     "Enqueue a new download",
	}, func(ctx context.Context, input *struct {
		Body models.EnqueueDownloadRequest
	}) (*struct {
		Body models.EnqueueDownloadResponse
	}, error) {
		res, err := h.Svc.Enqueue(ctx, input.Body)
		if err != nil {
			return nil, err
		}
		return &struct {
			Body models.EnqueueDownloadResponse
		}{Body: res}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "getDownload",
		Method:      http.MethodGet,
		Path:        "/downloads/{id}",
		Summary:     "Get a download by ID",
	}, func(ctx context.Context, input *struct {
		ID string `path:"id"`
	}) (*struct{ Body *models.Download }, error) {
		d, err := h.Svc.Get(ctx, input.ID)
		if err != nil {
			if err == errs.ErrNotFound {
				return nil, huma.Error404NotFound("download not found")
			}
			return nil, err
		}
		return &struct{ Body *models.Download }{Body: d}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "listDownloads",
		Method:      http.MethodGet,
		Path:        "/downloads",
		Summary:     "List downloads",
	}, func(ctx context.Context, input *struct {
		Status string `query:"status"`
		Queue  string `query:"queue"`
		Tags   string `query:"tags"`
		Search string `query:"q"`
		Limit  string `query:"limit"`
		Offset string `query:"offset"`
		Order  string `query:"order"`
		Desc   bool   `query:"desc"`
	}) (*struct{ Body []models.Download }, error) {
		var opts models.ListDownloadsOptions
		if input.Status != "" {
			opts.Statuses = []models.DownloadStatus{models.DownloadStatus(input.Status)}
		}
		if input.Queue != "" {
			opts.QueueIDs = []string{input.Queue}
		}
		if input.Tags != "" {
			for _, t := range strings.Split(input.Tags, ",") {
				if s := strings.TrimSpace(t); s != "" {
					opts.Tags = append(opts.Tags, s)
				}
			}
		}
		opts.Search = input.Search
		opts.OrderBy = input.Order
		opts.OrderDesc = input.Desc

		limit := 0
		if input.Limit != "" {
			var err error
			limit, err = strconv.Atoi(input.Limit)
			if err != nil {
				return nil, huma.Error400BadRequest("invalid limit parameter")
			}
		}

		offset := 0
		if input.Offset != "" {
			var err error
			offset, err = strconv.Atoi(input.Offset)
			if err != nil {
				return nil, huma.Error400BadRequest("invalid offset parameter")
			}
		}
		list, err := h.Svc.List(ctx, opts, limit, offset)
		if err != nil {
			return nil, err
		}
		return &struct{ Body []models.Download }{Body: list}, nil
	})

	huma.Register(api, huma.Operation{OperationID: "countDownloads", Method: http.MethodGet, Path: "/downloads/count"}, func(ctx context.Context, input *struct {
		Status string `query:"status"`
		Queue  string `query:"queue"`
		Tags   string `query:"tags"`
		Search string `query:"q"`
	}) (*struct{ Body int }, error) {
		var opts models.ListDownloadsOptions
		if input.Status != "" {
			opts.Statuses = []models.DownloadStatus{models.DownloadStatus(input.Status)}
		}
		if input.Queue != "" {
			opts.QueueIDs = []string{input.Queue}
		}
		if input.Tags != "" {
			for _, t := range strings.Split(input.Tags, ",") {
				if s := strings.TrimSpace(t); s != "" {
					opts.Tags = append(opts.Tags, s)
				}
			}
		}
		opts.Search = input.Search
		c, err := h.Svc.Count(ctx, opts)
		if err != nil {
			return nil, err
		}
		return &struct{ Body int }{Body: c}, nil
	})

	huma.Register(api, huma.Operation{OperationID: "startDownload", Method: http.MethodPost, Path: "/downloads/{id}/start"}, func(ctx context.Context, input *struct {
		ID string `path:"id"`
	}) (*struct{}, error) {
		return &struct{}{}, h.Svc.Start(ctx, input.ID)
	})
	huma.Register(api, huma.Operation{OperationID: "pauseDownload", Method: http.MethodPost, Path: "/downloads/{id}/pause"}, func(ctx context.Context, input *struct {
		ID string `path:"id"`
	}) (*struct{}, error) {
		return &struct{}{}, h.Svc.Pause(ctx, input.ID)
	})
	huma.Register(api, huma.Operation{OperationID: "resumeDownload", Method: http.MethodPost, Path: "/downloads/{id}/resume"}, func(ctx context.Context, input *struct {
		ID string `path:"id"`
	}) (*struct{}, error) {
		return &struct{}{}, h.Svc.Resume(ctx, input.ID)
	})
	huma.Register(api, huma.Operation{OperationID: "cancelDownload", Method: http.MethodPost, Path: "/downloads/{id}/cancel"}, func(ctx context.Context, input *struct {
		ID string `path:"id"`
	}) (*struct{}, error) {
		return &struct{}{}, h.Svc.Cancel(ctx, input.ID)
	})
	huma.Register(api, huma.Operation{OperationID: "deleteDownload", Method: http.MethodDelete, Path: "/downloads/{id}"}, func(ctx context.Context, input *struct {
		ID string `path:"id"`
	}) (*struct{}, error) {
		return &struct{}{}, h.Svc.Remove(ctx, input.ID, true)
	})

	huma.Register(api, huma.Operation{OperationID: "updateDownloadConfig", Method: http.MethodPut, Path: "/downloads/{id}/config"}, func(ctx context.Context, input *struct {
		ID   string `path:"id"`
		Body models.DownloadConfig
	}) (*struct{}, error) {
		return &struct{}{}, h.Svc.UpdateConfig(ctx, input.ID, input.Body)
	})
	huma.Register(api, huma.Operation{OperationID: "updateDownloadRequest", Method: http.MethodPut, Path: "/downloads/{id}/request"}, func(ctx context.Context, input *struct {
		ID   string `path:"id"`
		Body models.RequestOptions
	}) (*struct{}, error) {
		return &struct{}{}, h.Svc.UpdateRequest(ctx, input.ID, input.Body)
	})
	huma.Register(api, huma.Operation{OperationID: "setDownloadPriority", Method: http.MethodPost, Path: "/downloads/{id}/priority"}, func(ctx context.Context, input *struct {
		ID   string `path:"id"`
		Body *struct {
			Priority int `json:"priority"`
		}
	}) (*struct{}, error) {
		return &struct{}{}, h.Svc.SetPriority(ctx, input.ID, input.Body.Priority)
	})
	huma.Register(api, huma.Operation{OperationID: "addDownloadTags", Method: http.MethodPost, Path: "/downloads/{id}/tags"}, func(ctx context.Context, input *struct {
		ID   string `path:"id"`
		Body *struct {
			Tags []string `json:"tags"`
		}
	}) (*struct{}, error) {
		return &struct{}{}, h.Svc.AddTags(ctx, input.ID, input.Body.Tags...)
	})
	huma.Register(api, huma.Operation{OperationID: "removeDownloadTags", Method: http.MethodDelete, Path: "/downloads/{id}/tags"}, func(ctx context.Context, input *struct {
		ID   string `path:"id"`
		Body *struct {
			Tags []string `json:"tags"`
		}
	}) (*struct{}, error) {
		return &struct{}{}, h.Svc.RemoveTags(ctx, input.ID, input.Body.Tags...)
	})

	// Assign queue
	huma.Register(api, huma.Operation{OperationID: "assignQueue", Method: http.MethodPost, Path: "/downloads/{id}/queue"}, func(ctx context.Context, input *struct {
		ID   string `path:"id"`
		Body *struct {
			QueueID string `json:"queue_id"`
		}
	}) (*struct{}, error) {
		return &struct{}{}, h.Svc.AssignQueue(ctx, input.ID, input.Body.QueueID)
	})
}

// SSE route removed for now to avoid adapter-specific context use.
