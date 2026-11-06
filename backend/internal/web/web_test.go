package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

var site = fstest.MapFS{
	"index.html":            {Data: []byte(`<!doctype html><div id="root"></div>`)},
	"assets/index-abc1.js":  {Data: []byte(`console.log(1)`)},
	"assets/index-abc1.css": {Data: []byte(`body{}`)},
	"favicon.svg":           {Data: []byte(`<svg/>`)},
}

func get(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestHandler(t *testing.T) {
	h := Handler(site)
	cases := []struct {
		name, target string
		status       int
		body, ctype  string
		cacheControl string
	}{
		{"root", "/", 200, `<div id="root">`, "text/html", "no-cache"},
		{"index directly", "/index.html", 301, "", "", ""}, // ServeFile canonicalizes to /
		{"hashed asset", "/assets/index-abc1.js", 200, "console.log", "javascript", "immutable"},
		{"stylesheet", "/assets/index-abc1.css", 200, "body{}", "text/css", "immutable"},
		{"favicon", "/favicon.svg", 200, "<svg/>", "image/svg+xml", "max-age=3600"},
		{"client route", "/family", 200, `<div id="root">`, "text/html", "no-cache"},
		{"nested client route", "/events/123", 200, `<div id="root">`, "text/html", "no-cache"},
		{"missing asset", "/assets/old-xyz.js", 404, "", "", ""},
		{"api is never the app", "/api/unknown", 404, "", "", ""},
		{"auth is never the app", "/auth/unknown", 404, "", "", ""},
		// net/http refuses ".." in file-serving paths outright.
		{"path traversal", "/../../etc/passwd", 400, "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := get(h, http.MethodGet, c.target)
			if rec.Code != c.status {
				t.Fatalf("status %d, want %d", rec.Code, c.status)
			}
			if c.body != "" && !strings.Contains(rec.Body.String(), c.body) {
				t.Errorf("body %q lacks %q", rec.Body.String(), c.body)
			}
			if c.ctype != "" && !strings.Contains(rec.Header().Get("Content-Type"), c.ctype) {
				t.Errorf("Content-Type = %q, want %q", rec.Header().Get("Content-Type"), c.ctype)
			}
			if c.cacheControl != "" && !strings.Contains(rec.Header().Get("Cache-Control"), c.cacheControl) {
				t.Errorf("Cache-Control = %q, want %q", rec.Header().Get("Cache-Control"), c.cacheControl)
			}
		})
	}
}

func TestHandlerMethods(t *testing.T) {
	h := Handler(site)
	if rec := get(h, http.MethodHead, "/"); rec.Code != 200 {
		t.Errorf("HEAD / = %d", rec.Code)
	}
	if rec := get(h, http.MethodPost, "/"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST / = %d", rec.Code)
	}
}

func TestHandlerWithoutBuild(t *testing.T) {
	h := Handler(fstest.MapFS{".gitkeep": {}})
	rec := get(h, http.MethodGet, "/")
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "frontend not built") {
		t.Errorf("got %d %q", rec.Code, rec.Body.String())
	}
}
