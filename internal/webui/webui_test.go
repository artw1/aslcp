package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w
}

func TestPlaceholderWhenNotBuilt(t *testing.T) {
	handlers := []http.Handler{NewHandler(fstest.MapFS{".gitkeep": {}})}
	// The embedded dist/ only holds .gitkeep until `make web` has run.
	if _, err := fs.Stat(dist, "dist/index.html"); err != nil {
		handlers = append(handlers, Handler())
	}
	for _, h := range handlers {
		w := get(h, "/")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "UI not built") || !strings.Contains(w.Body.String(), "make web") {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		if w := get(h, "/some/spa/route"); !strings.Contains(w.Body.String(), "UI not built") {
			t.Fatal("placeholder not served on deep route")
		}
	}
}

func TestSPAFallback(t *testing.T) {
	h := NewHandler(fstest.MapFS{
		"index.html":     {Data: []byte("<html>app</html>")},
		"assets/app.js":  {Data: []byte("console.log(1)")},
		"favicon.svg":    {Data: []byte("<svg/>")},
		".gitkeep":       {},
		"assets/.hidden": {Data: []byte("x")},
	})
	if w := get(h, "/"); w.Code != 200 || w.Body.String() != "<html>app</html>" {
		t.Fatalf("/: %d %q", w.Code, w.Body.String())
	}
	if w := get(h, "/favorites/edit"); w.Body.String() != "<html>app</html>" {
		t.Fatal("SPA fallback failed")
	}
	w := get(h, "/assets/app.js")
	if w.Body.String() != "console.log(1)" || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("asset: %q %v", w.Body.String(), w.Header())
	}
	if w := get(h, "/favicon.svg"); w.Body.String() != "<svg/>" {
		t.Fatal("static file")
	}
	if w := get(h, "/assets/missing.js"); w.Code != 404 {
		t.Fatalf("missing asset: %d", w.Code)
	}
	if w := get(h, "/.gitkeep"); w.Body.String() != "<html>app</html>" {
		t.Fatal("dotfile served")
	}
	if w := get(h, "/../../etc/passwd"); w.Body.String() != "<html>app</html>" {
		t.Fatal("traversal")
	}
	if w := get(h, "/index.html"); w.Code != 200 || w.Body.String() != "<html>app</html>" {
		t.Fatalf("index.html direct: %d", w.Code)
	}
}

func TestIndexNotCacheable(t *testing.T) {
	h := NewHandler(fstest.MapFS{
		"index.html":    {Data: []byte("<html>app</html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	})
	for _, p := range []string{"/", "/index.html", "/favorites/edit"} {
		if cc := get(h, p).Header().Get("Cache-Control"); cc != "no-cache" {
			t.Fatalf("%s: Cache-Control %q", p, cc)
		}
	}
	if cc := get(h, "/assets/app.js").Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("asset: Cache-Control %q", cc)
	}
}
