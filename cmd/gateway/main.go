package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/company/api-key/internal/app"
	"github.com/company/api-key/internal/config"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration error", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	application, err := app.New(ctx, cfg, logger, app.Options{})
	if err != nil {
		logger.Error("startup failed", "error", err)
		os.Exit(1)
	}

	runErr := application.Server.Run(ctx)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := application.Close(shutdownCtx); err != nil {
		logger.Error("shutdown error", "error", err)
	}
	if runErr != nil {
		logger.Error("server stopped", "error", runErr)
		os.Exit(1)
	}
}
