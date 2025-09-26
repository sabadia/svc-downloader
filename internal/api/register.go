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
	api := huma.NewAPI(huma.DefaultConfig("svc-downloader", "1.0.0"), adapter)
	routes.Register(api, d.DownloadService())
	routes.RegisterQueues(api, d.QueueService())
	routes.RegisterEvents(api, d.EventPublisher())
	return r, api
}
