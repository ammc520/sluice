package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/company/api-key/internal/config"
	"github.com/company/api-key/internal/server"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration error", "error", err)
		os.Exit(1)
	}

	srv := server.New(cfg, server.Dependencies{Logger: logger})
	if err := srv.Run(context.Background()); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
