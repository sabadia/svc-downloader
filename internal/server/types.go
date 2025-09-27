package server

import "context"

// HTTPServer defines the minimal interface provided by the downloader server runtime.
// It enables consumers (e.g., CLI) to depend on an abstraction rather than a concrete type.
type HTTPServer interface {
	Addr() string
	RunForeground(ctx context.Context) error
	Close() error
}
