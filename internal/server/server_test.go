package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSPAHandlerServesIndexForNestedRoute(t *testing.T) {
	handler := newSPAHandler(fstest.MapFS{
		"index.html":         {Data: []byte("<html>spa</html>")},
		"assets/app.js":      {Data: []byte("console.log('ok')")},
		"assets/app.css":     {Data: []byte("body{}")},
		"dashboard/index.js": {Data: []byte("nested")},
	})

	req := httptest.NewRequest(http.MethodGet, "/runs/workflow-123", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), "<html>spa</html>") {
		t.Fatalf("expected index html, got %s", res.Body.String())
	}
}

func TestSPAHandlerServesExistingAsset(t *testing.T) {
	handler := newSPAHandler(fstest.MapFS{
		"index.html":    {Data: []byte("<html>spa</html>")},
		"assets/app.js": {Data: []byte("console.log('asset')")},
	})

	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), "console.log('asset')") {
		t.Fatalf("expected asset body, got %s", res.Body.String())
	}
}

func TestDevProxyRewritesNestedRouteToIndex(t *testing.T) {
	var requestedPath string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		_, _ = io.WriteString(w, "vite")
	}))
	defer target.Close()

	handler := newDevProxy(target.URL, slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodGet, "/runs/workflow-123", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
	if requestedPath != "/" {
		t.Fatalf("expected nested route to proxy to /, got %q", requestedPath)
	}
}

func TestDevProxyKeepsAssetPath(t *testing.T) {
	var requestedPath string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		_, _ = io.WriteString(w, "asset")
	}))
	defer target.Close()

	handler := newDevProxy(target.URL, slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
	if requestedPath != "/assets/app.js" {
		t.Fatalf("expected asset path to be preserved, got %q", requestedPath)
	}
}

func TestShouldServeDevSPAIndex(t *testing.T) {
	cases := []struct {
		name string
		path string
		want bool
	}{
		{name: "root", path: "/", want: false},
		{name: "api", path: "/api/workflows", want: false},
		{name: "nested route", path: "/runs/123", want: true},
		{name: "designer route", path: "/workflows/abc/designer", want: true},
		{name: "asset", path: "/assets/index.js", want: false},
		{name: "vite client", path: "/@vite/client", want: false},
		{name: "vite ping", path: "/__vite_ping", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if got := shouldServeDevSPAIndex(req); got != tc.want {
				t.Fatalf("expected %v, got %v", tc.want, got)
			}
		})
	}
}
