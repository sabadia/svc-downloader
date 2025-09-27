package config

import (
	"os"
	"path/filepath"
	"time"
)

// Config holds global configuration for the downloader service
// Keep it minimal and extensible; values can be overridden via CLI flags env, etc.
type Config struct {
	HTTPPort           int
	DataDir            string
	BadgerDir          string
	MaxBodyBytes       int64
	DefaultQueueID     string
	GracefulSecs       time.Duration
	GlobalRateLimitBPS int64
}

func Default() Config {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return Config{
		HTTPPort:           8089,
		DataDir:            filepath.Join(home, ".slog", "downloader", "data"),
		BadgerDir:          filepath.Join(home, ".slog", "downloader", "data", "badger"),
		MaxBodyBytes:       25 << 20,
		DefaultQueueID:     "main",
		GracefulSecs:       10 * time.Second,
		GlobalRateLimitBPS: 0,
	}
}
