package deps

import (
	"github.com/sabadia/svc-downloader/internal/models"
	"github.com/sabadia/svc-downloader/internal/service"
)

type Container struct {
	Downloads *service.DownloadServiceImpl
	Queues    *service.QueueServiceImpl
	Events    models.EventPublisher
}

func New(downloads *service.DownloadServiceImpl, queues *service.QueueServiceImpl, events models.EventPublisher) *Container {
	return &Container{Downloads: downloads, Queues: queues, Events: events}
}

func (c *Container) DownloadService() models.DownloadService { return c.Downloads }
func (c *Container) QueueService() models.QueueService       { return c.Queues }
func (c *Container) EventPublisher() models.EventPublisher   { return c.Events }
