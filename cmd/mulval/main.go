package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/urfave/cli/v3"
	"go.uber.org/multierr"
	"go.uber.org/zap"

	"github.com/cvewatcher/mulval/global"
	"github.com/cvewatcher/mulval/pkg/config"
	"github.com/cvewatcher/mulval/pkg/server"
)

var (
	Version = "dev"
	Commit  = ""
	Date    = ""
	BuiltBy = ""
)

func main() {
	cmd := &cli.Command{
		Name:  "MulVal",
		Usage: "Execute MulVal as a Service.",
		Flags: []cli.Flag{
			cli.VersionFlag,
			cli.HelpFlag,
			&cli.IntFlag{
				Name:     "port",
				Aliases:  []string{"p"},
				Sources:  cli.EnvVars("PORT"),
				Value:    8080,
				Usage:    "Define the API server port to listen on (gRPC+HTTP).",
				OnlyOnce: true,
				Local:    true,
			},
			&cli.BoolFlag{
				Name:     "swagger",
				Sources:  cli.EnvVars("SWAGGER"),
				Value:    false,
				Usage:    "If set, turns on the API gateway swagger on `/swagger`.",
				OnlyOnce: true,
				Local:    true,
			},
			&cli.BoolFlag{
				Name:     "ui",
				Sources:  cli.EnvVars("UI"),
				Value:    false,
				Usage:    "If set, turns on the UI on `/ui`.",
				OnlyOnce: true,
				Local:    true,
			},
			&cli.StringFlag{
				Name:      "config",
				Sources:   cli.EnvVars("CONFIG"),
				Value:     "config.yaml",
				TakesFile: true,
				OnlyOnce:  true,
				Local:     true,
			},
			config.ConfigFlag,
		},
		Commands: []*cli.Command{
			{
				Name: "schema",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "output",
						Aliases: []string{"o"},
						Usage:   "The output file name.",
						Value:   "schema.json",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					o := cmd.String("output")
					b, err := config.New().Schema()
					if err != nil {
						return err
					}
					return os.WriteFile(o, b, 0600)
				},
			},
		},
		Action: run,
		Authors: []any{
			"CVEWatcher's authors and contributors",
		},
		Version: Version,
		Metadata: map[string]any{
			"version": Version,
			"commit":  Commit,
			"date":    Date,
			"builtBy": BuiltBy,
		},
	}

	ctx := context.Background()
	if err := cmd.Run(ctx, os.Args); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, cmd *cli.Command) (err error) {
	// Pre-flight global configuration
	global.Version = Version

	port := cmd.Int("port")
	sw := cmd.Bool("swagger")
	ui := cmd.Bool("ui")
	global.Config, err = config.Load(cmd)
	if err != nil {
		return err
	}

	// Set up OpenTelemetry
	otelShutdown, err := global.SetupOTelSDK(ctx)
	if err != nil {
		return err
	}
	// Handle shutdown properly so nothing leaks
	defer func() {
		err = multierr.Append(err, otelShutdown(context.WithoutCancel(ctx)))
	}()

	logger := global.Log()

	// Create context that listens for the interrupt signal from the OS
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger.Info(ctx, "setting up services")
	if err := multierr.Combine(
		global.InitMetrics(),
		global.InitPostgreSQL(),
		global.InitNatsManager(ctx),
		global.InitExecutor(ctx),
	); err != nil {
		return err
	}

	if global.Config.Storage.Migrate {
		logger.Info(ctx, "running PostgreSQL migrations")
		if err := global.GetPgSQLManager().Migrate(ctx); err != nil {
			return err
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Launch API server
	logger.Info(ctx, "starting API server",
		zap.Int("port", port),
		zap.Bool("swagger", sw),
		zap.Bool("ui", ui),
	)
	srv := server.NewServer(server.Options{
		Port:    port,
		Swagger: sw,
		UI:      ui,
	})
	if err := srv.Run(ctx); err != nil {
		return err
	}

	// Listen for the interrupt signal
	<-ctx.Done()

	// Restore default behavior on the interrupt signal
	stop()
	logger.Info(ctx, "shutting down gracefully")

	// Give in-flight runner goroutines time to write their final state.
	// They use rc.Store (context.WithoutCancel) so DB writes will complete
	// as long as we don't exit immediately.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.Stop(shutdownCtx)                  // grpcServer.GracefulStop()
		global.GetExecutor().Wait(shutdownCtx) // wait for in-flight goroutines to end
		global.GetPgSQLManager().Close(shutdownCtx)
		global.GetPgSQLManager().Close(shutdownCtx)
	}()

	select {
	case <-done:
		logger.Info(context.WithoutCancel(ctx), "shutdown complete")
	case <-shutdownCtx.Done():
		logger.Warn(context.WithoutCancel(ctx), "shutdown grace period exceeded, forcing exit")
		os.Exit(1)
	}

	return nil
}
