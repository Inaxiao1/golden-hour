package transport

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type staticAsset struct {
	data, zipped []byte
	etag, kind   string
}
type staticFiles struct {
	assets          map[string]staticAsset
	version, bundle string
}

// Only public runtime files enter this map. Read/compress once at startup,
// never on a simulation tick or an asset request. Tests and sources stay private.
func loadStatic(root string) (*staticFiles, error) {
	files := map[string][]byte{}
	for _, name := range []string{"index.html", "i18n-zh.js", "dist/game.js"} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return nil, err
		}
		files[name] = data
	}
	err := filepath.WalkDir(filepath.Join(root, "client"), func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".test.js") || !(strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".css")) {
			return nil
		}
		rel, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(file)
		if err == nil {
			files[filepath.ToSlash(rel)] = data
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	if data, err := os.ReadFile(filepath.Join(root, "assets", "armoury.json")); err == nil {
		files["assets/armoury.json"] = data
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		fmt.Fprintf(hash, "%s\x00%d\x00", name, len(files[name]))
		hash.Write(files[name])
	}
	s := &staticFiles{assets: make(map[string]staticAsset), version: fmt.Sprintf("%x", hash.Sum(nil)[:12]), bundle: fmt.Sprintf("%x", sha256.Sum256(files["dist/game.js"]))}
	html := strings.ReplaceAll(string(files["index.html"]), "__GH_BUNDLE_VERSION__", s.bundle)
	for _, prefix := range []string{"./client/", "./i18n-zh.js"} {
		html = strings.ReplaceAll(html, prefix, "/assets/"+s.version+"/"+strings.TrimPrefix(prefix, "./"))
	}
	files["index.html"] = []byte(html)
	for _, name := range names {
		data := files[name]
		var compressed bytes.Buffer
		writer, _ := gzip.NewWriterLevel(&compressed, gzip.DefaultCompression)
		writer.Write(data)
		writer.Close()
		kind := mime.TypeByExtension(path.Ext(name))
		if kind == "" {
			kind = "application/octet-stream"
		}
		asset := staticAsset{data: data, etag: fmt.Sprintf(`"%x"`, sha256.Sum256(data)), kind: kind}
		if compressed.Len() < len(data) {
			asset.zipped = compressed.Bytes()
		}
		s.assets[name] = asset
	}
	return s, nil
}
func acceptsGzip(value string) bool {
	for _, item := range strings.Split(value, ",") {
		fields := strings.Split(strings.TrimSpace(item), ";")
		if fields[0] != "gzip" {
			continue
		}
		allowed := true
		for _, field := range fields[1:] {
			field = strings.TrimSpace(field)
			if strings.HasPrefix(field, "q=") {
				var q float64
				if _, err := fmt.Sscanf(field, "q=%f", &q); err != nil || q <= 0 {
					allowed = false
				}
			}
		}
		if allowed {
			return true
		}
	}
	return false
}
func (s *staticFiles) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	immutable := false
	if strings.HasPrefix(name, "assets/"+s.version+"/") {
		name = strings.TrimPrefix(name, "assets/"+s.version+"/")
		immutable = true
	}
	if name == "" {
		name = "index.html"
	}
	asset, ok := s.assets[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", asset.kind)
	w.Header().Set("Vary", "Accept-Encoding")
	if immutable && name != "index.html" {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	data, etag := asset.data, asset.etag
	if len(asset.zipped) > 0 && acceptsGzip(r.Header.Get("Accept-Encoding")) {
		data = asset.zipped
		etag = strings.TrimSuffix(etag, `"`) + `-gzip"`
		w.Header().Set("Content-Encoding", "gzip")
	}
	w.Header().Set("ETag", etag)
	http.ServeContent(w, r, path.Base(name), time.Time{}, bytes.NewReader(data))
}
