package cli

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/danielgtaylor/huma/v2/humacli"
	"github.com/sabadia/svc-downloader/internal/config"
	"github.com/sabadia/svc-downloader/internal/server"
	"github.com/spf13/cobra"
)

func Run() {
	cli := humacli.New(func(hooks humacli.Hooks, opts *Options) {
		hooks.OnStart(func() {
			_ = os.MkdirAll(opts.DataDir, 0o755)
		})
	})
	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the downloader server",
		Run: humacli.WithOptions(func(cmd *cobra.Command, args []string, opts *Options) {
			applyDynamicDefaults(opts)
			cfg := toConfig(opts)

			if !opts.Daemonize {
				_ = writePIDFile(opts.PIDFile, os.Getpid())
				defer func() { _ = removePIDFile(opts.PIDFile) }()
			}

			srv, err := server.New(cfg)
			if err != nil {
				log.Fatalf("failed to init server: %v", err)
			}
			defer func(srv *server.Server) {
				err := srv.Close()
				if err != nil {
					fmt.Printf("Error closing server: %v\n", err)
				}
			}(srv)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			stop := make(chan os.Signal, 1)
			signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-stop
				cancel()
			}()

			_ = srv.RunForeground(ctx)
		}),
	}

	// Commands
	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Start the downloader server",
		Run: humacli.WithOptions(func(cmd *cobra.Command, args []string, opts *Options) {
			applyDynamicDefaults(opts)
			cfg := toConfig(opts)
			if opts.GracefulShutdownSecs == 0 {
				opts.GracefulShutdownSecs = int(cfg.GracefulSecs / time.Second)
			}
			if err := startDaemon(opts); err != nil {
				log.Fatal(err)
			}

			fmt.Println("Started svc-downloader in background")

		}),
	}

	stopCmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the downloader server (daemon)",
		Run: humacli.WithOptions(func(cmd *cobra.Command, args []string, opts *Options) {
			applyDynamicDefaults(opts)
			def := config.Default()
			if opts.GracefulShutdownSecs == 0 {
				opts.GracefulShutdownSecs = int(def.GracefulSecs / time.Second)
			}
			if err := stopDaemon(opts, opts.GracefulShutdownSecs); err != nil {
				log.Fatal(err)
			}
		}),
	}

	restartCmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the downloader server (daemon)",
		Run: humacli.WithOptions(func(cmd *cobra.Command, args []string, opts *Options) {
			applyDynamicDefaults(opts)
			def := config.Default()
			if opts.GracefulShutdownSecs == 0 {
				opts.GracefulShutdownSecs = int(def.GracefulSecs / time.Second)
			}
			_ = stopDaemon(opts, opts.GracefulShutdownSecs)
			if err := initPidFile(opts); err != nil {
				log.Fatal(err)
			}
		}),
	}

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show server status",
		Run: humacli.WithOptions(func(cmd *cobra.Command, args []string, opts *Options) {
			applyDynamicDefaults(opts)
			pid, err := readPID(opts.PIDFile)
			if err != nil || pid <= 0 {
				println("svc-downloader: not running")
				return
			}
			if processAlive(pid) {
				println("svc-downloader: running (pid ", pid, ")")
			} else {
				println("svc-downloader: stale pid file (pid ", pid, " not alive)")
			}
		}),
	}

	cli.Root().AddCommand(serveCmd, startCmd, stopCmd, restartCmd, statusCmd)
	cli.Run()
}
