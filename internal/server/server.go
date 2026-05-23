package server

import (
	"bytes"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/prasenjit-net/orchestra/internal/api"
	"github.com/prasenjit-net/orchestra/internal/config"
	"github.com/prasenjit-net/orchestra/internal/livebus"
	"github.com/prasenjit-net/orchestra/internal/version"
	"github.com/prasenjit-net/orchestra/internal/workflow"
)

type Options struct {
	DevMode  bool
	UIFS     fs.FS
	Live     *livebus.Bus
	Workflow *workflow.Service
}

type App struct {
	cfg     config.Config
	logger  *slog.Logger
	build   version.Info
	options Options
}

func New(cfg config.Config, logger *slog.Logger, build version.Info, options Options) (*App, error) {
	return &App{cfg: cfg, logger: logger, build: build, options: options}, nil
}

func (a *App) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Heartbeat("/livez"))
	r.Use(requestLogger(a.logger))

	r.Mount("/api", api.NewRouter(a.cfg, a.logger, a.build, a.options.Live, a.options.Workflow))

	if a.options.DevMode && strings.TrimSpace(a.cfg.UI.DevProxyURL) != "" {
		r.Handle("/*", newDevProxy(a.cfg.UI.DevProxyURL, a.logger))
		return r
	}

	distFS, err := fs.Sub(a.options.UIFS, "ui/dist")
	if err != nil {
		a.logger.Error("embedded ui not available", "error", err)
		r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "embedded UI missing; run `make build-ui` before building the binary", http.StatusServiceUnavailable)
		})
		return r
	}

	spa := newSPAHandler(distFS)
	r.Handle("/*", spa)

	return r
}

func newDevProxy(rawURL string, logger *slog.Logger) http.Handler {
	target, err := url.Parse(rawURL)
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, fmt.Sprintf("invalid UI dev proxy URL: %v", err), http.StatusInternalServerError)
		})
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, proxyErr error) {
		logger.Error("vite proxy error", "error", proxyErr)
		http.Error(w, "Vite dev server is unavailable. Start it with `make dev-ui` or `make dev-all`.", http.StatusBadGateway)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyReq := r.Clone(r.Context())
		if shouldServeDevSPAIndex(proxyReq) {
			proxyReq.URL.Path = "/"
		}
		proxy.ServeHTTP(w, proxyReq)
	})
}

type spaHandler struct {
	fsys       fs.FS
	fileServer http.Handler
}

func newSPAHandler(fsys fs.FS) http.Handler {
	return &spaHandler{
		fsys:       fsys,
		fileServer: http.FileServer(http.FS(fsys)),
	}
}

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cleanPath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if cleanPath == "." || cleanPath == "" {
		cleanPath = "index.html"
	}

	if fileExists(h.fsys, cleanPath) {
		h.fileServer.ServeHTTP(w, r)
		return
	}

	indexBytes, err := fs.ReadFile(h.fsys, "index.html")
	if err != nil {
		http.Error(w, "index.html missing from embedded UI", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(indexBytes))
}

func fileExists(fsys fs.FS, name string) bool {
	file, err := fsys.Open(name)
	if err != nil {
		return false
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return false
	}

	return !info.IsDir()
}

func shouldServeDevSPAIndex(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	cleanPath := path.Clean(r.URL.Path)
	if cleanPath == "/" || cleanPath == "." {
		return false
	}
	if strings.HasPrefix(cleanPath, "/api/") || cleanPath == "/api" {
		return false
	}
	if strings.HasPrefix(cleanPath, "/@") || strings.HasPrefix(cleanPath, "/__vite") {
		return false
	}
	base := path.Base(cleanPath)
	return !strings.Contains(base, ".")
}

func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			logger.Info("request complete",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration", time.Since(start).String(),
				"request_id", middleware.GetReqID(r.Context()),
			)
		})
	}
}
