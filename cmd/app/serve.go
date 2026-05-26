package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
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
	devMode        bool
	portFlag       int
	controllerFlag bool
	workerFlag     bool
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the HTTP server",
	RunE:  runServe,
}

func init() {
	serveCmd.Flags().BoolVar(&devMode, "dev", false, "Enable development mode and proxy UI requests to Vite")
	serveCmd.Flags().IntVarP(&portFlag, "port", "p", 0, "Override server port")
	serveCmd.Flags().BoolVar(&controllerFlag, "controller", false, "Enable controller role (HTTP API + UI)")
	serveCmd.Flags().BoolVar(&workerFlag, "worker", false, "Enable worker role (task executor)")
	_ = viper.BindPFlag("server.port", serveCmd.Flags().Lookup("port"))
}

func runServe(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(viper.GetViper())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	cfg.ConfigFilePath = viper.GetViper().ConfigFileUsed()
	if portFlag > 0 {
		cfg.Server.Port = portFlag
	}

	// Resolve roles: CLI flags take precedence over config; default is both.
	isController, isWorker := resolveRoles(cmd, cfg)

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

	// Register this node and start the heartbeat goroutine.
	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()

	nodeID := resolveNodeID(cfg.Node.ID)
	if workflowService != nil {
		hostname, _ := os.Hostname()
		if err := workflowService.RegisterNode(context.Background(), workflow.NodeInfo{
			ID:            nodeID,
			Role:          deriveRole(isController, isWorker),
			Address:       resolveNodeAddress(isController, cfg),
			Capabilities:  workflowService.ActivityNames(),
			MaxConcurrent: cfg.Node.MaxConcurrentTasks,
			Version:       buildInfo.Version,
			Hostname:      hostname,
		}); err != nil {
			logger.Warn("register node", "error", err)
		}
		defer func() { _ = workflowService.DeregisterNode(context.Background(), nodeID) }()
		go runHeartbeat(workerCtx, workflowService, nodeID, cfg.Node.Health.HeartbeatInterval)
	}

	// Start task poller on worker nodes.
	if isWorker && workflowService != nil {
		workflowService.Start(workerCtx)
	}

	restartCh := make(chan struct{}, 1)
	errCh := make(chan error, 1)

	var httpServer *http.Server
	var healthServer *http.Server

	if isController {
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

		appServer, err := server.New(cfg, logger, buildInfo, server.Options{
			DevMode:   devMode,
			UIFS:      uiFS,
			Live:      live,
			Workflow:  workflowService,
			RestartCh: restartCh,
		})
		if err != nil {
			return err
		}

		httpServer = &http.Server{
			Addr:         cfg.Server.Address(),
			Handler:      appServer.Handler(),
			ReadTimeout:  cfg.Server.ReadTimeout,
			WriteTimeout: cfg.Server.WriteTimeout,
			IdleTimeout:  cfg.Server.IdleTimeout,
		}
		go func() {
			logger.Info("starting server",
				"addr", httpServer.Addr,
				"env", cfg.App.Env,
				"dev_mode", devMode,
				"role", deriveRole(isController, isWorker),
			)
			if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
			}
		}()
	} else {
		// Worker-only: always start the minimal health server.
		healthServer = startHealthServer(cfg.Node.HealthAddr, logger)
		logger.Info("starting worker",
			"health_addr", cfg.Node.HealthAddr,
			"max_concurrent", cfg.Node.MaxConcurrentTasks,
		)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	restart := false
	select {
	case err := <-errCh:
		stopWorker()
		return err
	case <-restartCh:
		logger.Info("restart requested")
		restart = true
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	stopWorker()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	if httpServer != nil {
		_ = httpServer.Shutdown(shutdownCtx)
	}
	if healthServer != nil {
		_ = healthServer.Shutdown(shutdownCtx)
	}

	if restart {
		execSelf(logger)
	}
	return nil
}

// resolveRoles determines which subsystems to enable.
// CLI flags take precedence over config; if neither source enables anything, both default to true.
func resolveRoles(cmd *cobra.Command, cfg config.Config) (isController, isWorker bool) {
	controllerChanged := cmd.Flags().Changed("controller")
	workerChanged := cmd.Flags().Changed("worker")

	if controllerChanged || workerChanged {
		if controllerChanged {
			isController, _ = cmd.Flags().GetBool("controller")
		}
		if workerChanged {
			isWorker, _ = cmd.Flags().GetBool("worker")
		}
	} else {
		isController = cfg.Node.Controller
		isWorker = cfg.Node.Worker
	}

	if !isController && !isWorker {
		isController = true
		isWorker = true
	}
	return
}

func deriveRole(isController, isWorker bool) string {
	switch {
	case isController && isWorker:
		return "all"
	case isController:
		return "controller"
	default:
		return "worker"
	}
}

// resolveNodeID returns the configured ID or generates a random one.
func resolveNodeID(configured string) string {
	if configured != "" {
		return configured
	}
	return workflow.GenerateNodeID()
}

// resolveNodeAddress builds the http://host:port URI for this node.
// Controllers advertise the main HTTP server; workers advertise the health endpoint.
func resolveNodeAddress(isController bool, cfg config.Config) string {
	ip := resolveOutboundIP()
	if ip == "" {
		ip = "localhost"
	}
	var port int
	if isController {
		port = cfg.Server.Port
	} else {
		_, portStr, err := net.SplitHostPort(cfg.Node.HealthAddr)
		if err == nil {
			port, _ = strconv.Atoi(portStr)
		}
	}
	if port == 0 {
		return fmt.Sprintf("http://%s", ip)
	}
	return fmt.Sprintf("http://%s:%d", ip, port)
}

// resolveOutboundIP returns the primary outbound IP via a non-connecting UDP dial.
func resolveOutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

// runHeartbeat periodically updates last_seen_at in the nodes table.
func runHeartbeat(ctx context.Context, svc *workflow.Service, nodeID string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := svc.HeartbeatNode(ctx, nodeID); err != nil && !errors.Is(err, context.Canceled) {
				// non-fatal — log would spam; silently skip
				_ = err
			}
		}
	}
}

// startHealthServer starts a minimal HTTP server exposing GET /livez.
func startHealthServer(addr string, logger interface{ Info(string, ...any) }) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		logger.Info("starting health server", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// non-fatal for a health probe endpoint
			_ = err
		}
	}()
	return srv
}

func execSelf(logger interface{ Info(string, ...any); Error(string, ...any) }) {
	exe, err := os.Executable()
	if err != nil {
		logger.Error("restart: get executable", "error", err)
		return
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		logger.Error("restart: eval symlinks", "error", err)
		return
	}
	logger.Info("restarting process", "exe", exe)
	if err := syscall.Exec(exe, os.Args, os.Environ()); err != nil {
		logger.Error("restart: exec failed", "error", err)
	}
}
