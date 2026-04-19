package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	httpapi "github.com/admin/ai_project/api/http"
	"github.com/admin/ai_project/internal/app"
	"github.com/admin/ai_project/internal/config"
	"github.com/admin/ai_project/plugins/builtin"
	"go.uber.org/zap"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "./configs/config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()
	application, err := app.New(ctx, cfg)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "init app: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		_ = application.Close(context.Background())
	}()
	if err := builtin.RegisterAll(application.Registry); err != nil {
		application.Logger().Fatal("register builtin nodes failed", zap.Error(err))
	}
	if err := application.Registry.LoadManifests(ctx); err != nil {
		application.Logger().Warn("load manifests failed", zap.Error(err))
	}

	router := httpapi.New(application)
	server := &http.Server{
		Addr:         cfg.Server.Address,
		Handler:      router.Handler(),
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			application.Logger().Fatal("server exited", zap.Error(err))
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		application.Logger().Error("server shutdown failed", zap.Error(err))
	}
}
