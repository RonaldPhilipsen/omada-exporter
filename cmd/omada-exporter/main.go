// Command omada-exporter polls a TP-Link Omada controller and pushes the
// result as OpenTelemetry metrics. All logging goes to stderr.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/RonaldPhilipsen/omada-exporter/internal/collector"
	"github.com/RonaldPhilipsen/omada-exporter/internal/config"
	"github.com/RonaldPhilipsen/omada-exporter/internal/metrics"
	"github.com/RonaldPhilipsen/omada-exporter/internal/omadaapi"
	"github.com/RonaldPhilipsen/omada-exporter/internal/snapshot"
)

// version is set at build time with -X main.version=<value>.
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.FromEnv(os.Getenv)
	if err != nil {
		logger.Error("configuration failed", "error", err)
		os.Exit(1)
	}

	api, err := omadaapi.New(cfg)
	if err != nil {
		logger.Error("omada client setup failed", "error", err)
		os.Exit(1)
	}

	provider, err := metrics.NewProvider(ctx, version)
	if err != nil {
		logger.Error("metrics provider setup failed", "error", err)
		os.Exit(1)
	}

	// The recorder is needed by the collector and the collector's snapshot is
	// needed by the metrics, so the source function closes over the collector.
	// A callback can run before the collector exists, so the pointer is atomic
	// and a missing collector yields no snapshot.
	var colPtr atomic.Pointer[collector.Collector]
	rec, err := metrics.Register(provider.Meter("omada-exporter"),
		func() *snapshot.Snapshot {
			if c := colPtr.Load(); c != nil {
				return c.Snapshot()
			}
			return nil
		}, cfg.ClientMetrics)
	if err != nil {
		logger.Error("metrics registration failed", "error", err)
		os.Exit(1)
	}
	col := collector.New(api, rec, logger, collector.Options{
		PollInterval:     cfg.PollInterval,
		FirmwareInterval: cfg.FirmwarePollInterval,
		FirmwareDelay:    250 * time.Millisecond,
	})

	colPtr.Store(col)

	logger.Info("omada-exporter starting",
		"version", version,
		"base_url", cfg.BaseURL,
		"poll_interval", cfg.PollInterval,
		"firmware_poll_interval", cfg.FirmwarePollInterval,
		"client_metrics", cfg.ClientMetrics,
	)

	runErr := col.Run(ctx)

	// The signal context is already cancelled, so shut down from a fresh one.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := provider.Shutdown(shutdownCtx); err != nil {
		logger.Error("metrics shutdown failed", "error", err)
	}

	if runErr != nil {
		logger.Error("collector stopped with error", "error", runErr)
		os.Exit(1)
	}
	logger.Info("omada-exporter stopped")
}
