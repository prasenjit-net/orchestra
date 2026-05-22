package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/prasenjit-net/orchestra/internal/api"
	"github.com/prasenjit-net/orchestra/internal/config"
	"github.com/prasenjit-net/orchestra/internal/livebus"
	"github.com/prasenjit-net/orchestra/internal/logging"
	"github.com/prasenjit-net/orchestra/internal/server"
	"github.com/prasenjit-net/orchestra/internal/version"
	"github.com/prasenjit-net/orchestra/internal/workflow"
)

var (
	devMode  bool
	portFlag int
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the HTTP server",
	RunE:  runServe,
}

func init() {
	serveCmd.Flags().BoolVar(&devMode, "dev", false, "Enable development mode and proxy UI requests to Vite")
	serveCmd.Flags().IntVarP(&portFlag, "port", "p", 0, "Override server port")
	_ = viper.BindPFlag("server.port", serveCmd.Flags().Lookup("port"))
}

func runServe(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(viper.GetViper())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if portFlag > 0 {
		cfg.Server.Port = portFlag
	}

	logger := logging.New(cfg.Logging)
	buildInfo := version.Current()
	live := livebus.New()
	defer live.Close()

	workflowService, err := workflow.NewService(cfg.Workflow, logger, live)
	if err != nil {
		return fmt.Errorf("create workflow service: %w", err)
	}
	if workflowService != nil {
		defer workflowService.Close()
	}

	appServer, err := server.New(cfg, logger, buildInfo, server.Options{
		DevMode:  devMode,
		UIFS:     uiFS,
		Live:     live,
		Workflow: workflowService,
	})
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:         cfg.Server.Address(),
		Handler:      appServer.Handler(),
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}

	errCh := make(chan error, 1)
	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()
	publishHealth := func() {
		live.Publish(livebus.NewEvent("health.updated", "health", "api", api.BuildHealthResponse(cfg, buildInfo)))
	}
	publishHealth()
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				publishHealth()
			}
		}
	}()
	if workflowService != nil {
		workflowService.Start(workerCtx)
	}
	go func() {
		logger.Info("starting server",
			"addr", httpServer.Addr,
			"env", cfg.App.Env,
			"dev_mode", devMode,
			"ui_proxy", cfg.UI.DevProxyURL,
		)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-errCh:
		stopWorker()
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	stopWorker()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	return httpServer.Shutdown(shutdownCtx)
}
