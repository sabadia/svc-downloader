package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/sabadia/svc-downloader/internal/api/routes"
	"github.com/sabadia/svc-downloader/internal/models"
)

type Deps interface {
	DownloadService() models.DownloadService
	QueueService() models.QueueService
	EventPublisher() models.EventPublisher
}

func NewServer(d Deps) (http.Handler, huma.API) {
	r := chi.NewRouter()
	adapter := humachi.NewAdapter(r)

	// Configure API with OpenAPI support
	config := huma.DefaultConfig("svc-downloader", "1.0.0")
	config.DocsPath = "/openapi.yaml"
	config.OpenAPI = &huma.OpenAPI{
		Info: &huma.Info{
			Title:       "svc-downloader",
			Description: "A standalone HTTP microservice for resilient, segmented file downloads with queueing, rate limiting, retries/backoff, and progress events via Server-Sent Events (SSE).",
			Version:     "1.0.0",
		},
		Servers: []*huma.Server{
			{
				URL:         "http://localhost:8089",
				Description: "Development server",
			},
		},
	}

	api := huma.NewAPI(config, adapter)

	// Add OpenAPI endpoint
	r.Get("/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-yaml")
		w.WriteHeader(http.StatusOK)
		yamlData, err := api.OpenAPI().YAML()
		if err != nil {
			http.Error(w, "Failed to generate OpenAPI spec", http.StatusInternalServerError)
			return
		}
		w.Write(yamlData)
	})

	routes.Register(api, d.DownloadService())
	routes.RegisterQueues(api, d.QueueService())
	routes.RegisterEvents(api, d.EventPublisher())
	return r, api
}
