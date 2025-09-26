package config

import (
	"time"
)

// Config holds global configuration for the downloader service
// Keep it minimal and extensible; values can be overridden via CLI flags env, etc.
type Config struct {
	HTTPPort       int
	DataDir        string
	BadgerDir      string
	MaxBodyBytes   int64
	DefaultQueueID string
	GracefulSecs   time.Duration
}

func Default() Config {
	return Config{
		HTTPPort:       8089,
		DataDir:        "./data",
		BadgerDir:      "./data/badger",
		MaxBodyBytes:   25 << 20,
		DefaultQueueID: "main",
		GracefulSecs:   10 * time.Second,
	}
}
