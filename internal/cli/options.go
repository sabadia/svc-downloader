package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/sabadia/svc-downloader/internal/config"
)

// Options defines CLI options for the downloader service
// Values can be provided via flags or environment variables.
// Dynamic defaults are applied in code using config.Default().
type Options struct {
	// Server options
	Port                 int    `doc:"Port to listen on." short:"p"`
	DataDir              string `doc:"Base data directory used by the service (contains databases, logs, etc.)"`
	BadgerDir            string `doc:"Directory path for Badger DB files. Defaults under data dir."`
	MaxBodyBytes         int64  `doc:"Max request body size in bytes."`
	DefaultQueueID       string `doc:"Default queue ID to use when none specified."`
	GracefulShutdownSecs int    `doc:"Graceful shutdown timeout in seconds." default:"10"`
	GlobalRateLimitBPS   int64  `doc:"Global rate limit in bytes per second (0 disables)."`

	// Process control
	Daemonize bool   `doc:"Run in background (headless) and write PID/log files."`
	PIDFile   string `doc:"Path to PID file when running as a daemon."`
	LogFile   string `doc:"Path to log file when running as a daemon."`
}

func applyDynamicDefaults(o *Options) {
	def := config.Default()
	// Allow env to override port like previous behavior
	if p := os.Getenv("PORT"); p != "" {
		if port, err := strconv.Atoi(p); err == nil {
			o.Port = port
		}
	}
	if grl := os.Getenv("GLOBAL_RATE_LIMIT_BPS"); grl != "" {
		if v, err := strconv.ParseInt(grl, 10, 64); err == nil {
			o.GlobalRateLimitBPS = v
		}
	}
	if o.Port == 0 {
		o.Port = def.HTTPPort
	}
	if o.DataDir == "" {
		o.DataDir = def.DataDir
	}
	if o.BadgerDir == "" {
		// keep compatibility with previous default
		o.BadgerDir = def.BadgerDir
	}
	if o.MaxBodyBytes == 0 {
		o.MaxBodyBytes = def.MaxBodyBytes
	}
	if o.DefaultQueueID == "" {
		o.DefaultQueueID = def.DefaultQueueID
	}
	if o.GracefulShutdownSecs == 0 {
		o.GracefulShutdownSecs = int(def.GracefulSecs / time.Second)
	}
	if o.PIDFile == "" {
		o.PIDFile = filepath.Join(o.DataDir, "svc-downloader.pid")
	}
	if o.LogFile == "" {
		o.LogFile = filepath.Join(o.DataDir, "svc-downloader.log")
	}
}

func toConfig(o *Options) config.Config {
	return config.Config{
		HTTPPort:           o.Port,
		DataDir:            o.DataDir,
		BadgerDir:          o.BadgerDir,
		MaxBodyBytes:       o.MaxBodyBytes,
		DefaultQueueID:     o.DefaultQueueID,
		GracefulSecs:       time.Duration(o.GracefulShutdownSecs) * time.Second,
		GlobalRateLimitBPS: o.GlobalRateLimitBPS,
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

func buildArgsFromOptions(o *Options) []string {
	var args []string
	// Start with command name
	args = append(args, "serve")
	// Map options to flags; only include non-zero/empty values
	if o.Port != 0 {
		args = append(args, "--port", itoa(o.Port))
	}
	if o.DataDir != "" {
		args = append(args, "--data-dir", o.DataDir)
	}
	if o.BadgerDir != "" {
		args = append(args, "--badger-dir", o.BadgerDir)
	}
	if o.MaxBodyBytes != 0 {
		args = append(args, "--max-body-bytes", strconv.FormatInt(o.MaxBodyBytes, 10))
	}
	if o.DefaultQueueID != "" {
		args = append(args, "--default-queue-id", o.DefaultQueueID)
	}
	if o.GracefulShutdownSecs != 0 {
		args = append(args, "--graceful-shutdown-secs", strconv.Itoa(o.GracefulShutdownSecs))
	}
	if o.GlobalRateLimitBPS != 0 {
		args = append(args, "--global-rate-limit-bps", strconv.FormatInt(o.GlobalRateLimitBPS, 10))
	}
	args = append(args, "--daemonize="+strconv.FormatBool(o.Daemonize))
	// No daemon flag for child; it runs foreground under launchd/nohup
	return args
}
