package server

import (
	"bytes"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/prasenjit-net/orchestra/internal/api"
	"github.com/prasenjit-net/orchestra/internal/auth"
	"github.com/prasenjit-net/orchestra/internal/config"
	"github.com/prasenjit-net/orchestra/internal/livebus"
	"github.com/prasenjit-net/orchestra/internal/version"
	"github.com/prasenjit-net/orchestra/internal/workflow"
)

type Options struct {
	DevMode        bool
	UIFS           fs.FS
	Live           *livebus.Bus
	Workflow       *workflow.Service
	Auth           *auth.Service
	RestartCh      chan struct{}
	ConfigEditable bool
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
	r.Use(trustedRealIP(a.cfg.Auth.TrustedProxyCIDRs, a.logger))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Heartbeat("/livez"))
	r.Use(securityHeaders(a.options.DevMode))
	r.Use(limitRequestBody(4 << 20))
	r.Use(requestLogger(a.logger))

	r.Mount("/api", api.NewRouter(a.cfg, a.logger, a.build, api.RouterOptions{
		Live: a.options.Live, Workflow: a.options.Workflow, Auth: a.options.Auth,
		RestartCh: a.options.RestartCh, ConfigEditable: a.options.ConfigEditable,
	}))

	if extRouter, err := api.NewExtRouter(a.cfg, a.options.Workflow, a.options.Auth); err != nil {
		a.logger.Error("failed to create ext router", "error", err)
	} else {
		r.Mount("/ext", extRouter)
	}

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

func securityHeaders(devMode bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Referrer-Policy", "same-origin")
			w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			if !devMode {
				w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self' ws: wss:; worker-src 'self' blob:")
			}
			if r.TLS != nil {
				w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			if strings.HasPrefix(r.URL.Path, "/api") || strings.HasPrefix(r.URL.Path, "/ext") {
				w.Header().Set("Cache-Control", "no-store")
			}
			next.ServeHTTP(w, r)
		})
	}
}

func limitRequestBody(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil && r.Body != http.NoBody {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func trustedRealIP(cidrs []string, logger *slog.Logger) func(http.Handler) http.Handler {
	prefixes := parseTrustedPrefixes(cidrs, logger)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			peer, ok := parseRemoteAddress(r.RemoteAddr)
			if !ok || !isTrustedAddress(prefixes, peer) {
				next.ServeHTTP(w, r)
				return
			}
			r.RemoteAddr = clientAddress(forwardedAddressChain(r, peer), prefixes).String()
			next.ServeHTTP(w, r)
		})
	}
}

func parseTrustedPrefixes(cidrs []string, logger *slog.Logger) []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, len(cidrs))
	for _, raw := range cidrs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			logger.Warn("ignoring invalid trusted proxy CIDR", "cidr", raw)
			continue
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes
}

func isTrustedAddress(prefixes []netip.Prefix, address netip.Addr) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(address.Unmap()) {
			return true
		}
	}
	return false
}

func forwardedAddressChain(r *http.Request, peer netip.Addr) []netip.Addr {
	chain := make([]netip.Addr, 0, 4)
	for _, raw := range strings.Split(r.Header.Get("X-Forwarded-For"), ",") {
		if address, err := netip.ParseAddr(strings.TrimSpace(raw)); err == nil {
			chain = append(chain, address.Unmap())
		}
	}
	if len(chain) == 0 {
		if address, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil {
			chain = append(chain, address.Unmap())
		}
	}
	return append(chain, peer.Unmap())
}

func clientAddress(chain []netip.Addr, trustedPrefixes []netip.Prefix) netip.Addr {
	for index := len(chain) - 1; index > 0; index-- {
		if !isTrustedAddress(trustedPrefixes, chain[index]) {
			return chain[index]
		}
	}
	return chain[0]
}

func parseRemoteAddress(raw string) (netip.Addr, bool) {
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	address, err := netip.ParseAddr(strings.Trim(raw, "[]"))
	return address, err == nil
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
	if strings.HasPrefix(cleanPath, "/ext/") || cleanPath == "/ext" {
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
