package transport

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionedAssetCachingAndCompression(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	assets, err := loadStatic(root)
	if err != nil {
		t.Fatal(err)
	}
	get := func(url, encoding, etag string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", url, nil)
		req.Header.Set("Accept-Encoding", encoding)
		req.Header.Set("If-None-Match", etag)
		out := httptest.NewRecorder()
		assets.ServeHTTP(out, req)
		return out
	}
	page := get("/", "", "")
	if page.Code != 200 || page.Header().Get("Cache-Control") != "no-cache" || !strings.Contains(page.Body.String(), "/assets/"+assets.version+"/client/bootstrap.js") || strings.Contains(page.Body.String(), "__GH_BUNDLE_VERSION__") {
		t.Fatal("HTML version injection failed", page.Code)
	}
	url := "/assets/" + assets.version + "/dist/game.js"
	compressed := get(url, "gzip", "")
	if compressed.Code != 200 || compressed.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(compressed.Header().Get("Cache-Control"), "immutable") {
		t.Fatal("asset not cached/compressed", compressed.Header())
	}
	reader, err := gzip.NewReader(compressed.Body)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(assets.assets["dist/game.js"].data) {
		t.Fatal("gzip altered content")
	}
	if compressed.Body.Len() >= len(data)/2 {
		t.Fatal("bundle compression ineffective")
	}
	cached := get(url, "gzip", compressed.Header().Get("ETag"))
	if cached.Code != http.StatusNotModified || cached.Body.Len() != 0 {
		t.Fatal("conditional cache miss", cached.Code)
	}
	plain := get(url, "gzip;q=0", "")
	if plain.Header().Get("Content-Encoding") != "" || plain.Body.Len() != len(data) || plain.Header().Get("ETag") == compressed.Header().Get("ETag") {
		t.Fatal("representation negotiation failed")
	}
	for _, url := range []string{"/assets/old/dist/game.js", "/client/net.test.js", "/.git/config", "/.local/dev-key.pem", "/internal/game/maps/hotel.json", "/assets/" + assets.version + "/cmd/server/main.go"} {
		if response := get(url, "", ""); response.Code != 404 {
			t.Fatalf("exposed %s: %d", url, response.Code)
		}
	}
	unversioned := get("/client/net.js", "", "")
	if unversioned.Code != 200 || unversioned.Header().Get("Cache-Control") != "no-cache" {
		t.Fatal("mutable URL must revalidate")
	}
}

func TestGzipNegotiation(t *testing.T) {
	for _, test := range []struct {
		value string
		want  bool
	}{{"gzip, deflate, br", true}, {"gzip;q=0", false}, {"br", false}, {"gzip;q=0.0, br", false}, {"gzip;q=0.5", true}, {"gzip;q=invalid", false}} {
		if got := acceptsGzip(test.value); got != test.want {
			t.Errorf("%q: %v", test.value, got)
		}
	}
}
