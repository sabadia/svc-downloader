package events

import (
	"context"
	"sync"

	"github.com/sabadia/svc-downloader/internal/models"
)

type InMemoryPublisher struct {
	mu   sync.RWMutex
	subs map[models.DownloadEventType][]chan models.DownloadEvent
}

func NewInMemoryPublisher() *InMemoryPublisher {
	return &InMemoryPublisher{subs: make(map[models.DownloadEventType][]chan models.DownloadEvent)}
}

func (p *InMemoryPublisher) Publish(ctx context.Context, event models.DownloadEvent) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for t, chans := range p.subs {
		if t == event.Type {
			for _, ch := range chans {
				select {
				case ch <- event:
				default:
				}
			}
		}
	}
	return nil
}

func (p *InMemoryPublisher) Subscribe(ctx context.Context, types ...models.DownloadEventType) (<-chan models.DownloadEvent, func(), error) {
	ch := make(chan models.DownloadEvent, 64)
	p.mu.Lock()
	for _, t := range types {
		p.subs[t] = append(p.subs[t], ch)
	}
	p.mu.Unlock()
	cancel := func() {
		p.mu.Lock()
		for _, t := range types {
			subs := p.subs[t]
			for i := range subs {
				if subs[i] == ch {
					p.subs[t] = append(subs[:i], subs[i+1:]...)
					break
				}
			}
		}
		p.mu.Unlock()
		close(ch)
	}
	return ch, cancel, nil
}
