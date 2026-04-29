package compute

import (
"context"

"crypto/sha256"
"fmt"
"io"
"net/http"
"net/url"
"os"
"path/filepath"
"strings"
)

// resolveImage ensures imageRef is available as a local qcow2 file and returns
// its absolute path. If imageRef is already a local path it is returned as-is.
// If it is an HTTP(S) URL the image is downloaded into cacheDir, keyed by a
// SHA-256 of the URL so repeated calls for the same URL are no-ops.
func resolveImage(ctx context.Context, cacheDir, imageRef string) (string, error) {
if !isURL(imageRef) {
// Already a local path — validate it exists.
if _, err := os.Stat(imageRef); err != nil {
return "", fmt.Errorf("image path %q not found: %w", imageRef, err)
}
return imageRef, nil
}

if err := os.MkdirAll(cacheDir, 0o755); err != nil {
return "", fmt.Errorf("create image cache dir: %w", err)
}

dest := cachedImagePath(cacheDir, imageRef)

// Fast path: already cached.
if _, err := os.Stat(dest); err == nil {
return dest, nil
}

// Download to a temp file in the same directory, then rename atomically.
tmp, err := os.CreateTemp(cacheDir, ".dl-*")
if err != nil {
return "", fmt.Errorf("create temp file: %w", err)
}
tmpName := tmp.Name()
defer func() {
tmp.Close()
os.Remove(tmpName) // no-op if rename succeeded
}()

req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageRef, nil)
if err != nil {
return "", fmt.Errorf("build http request: %w", err)
}

resp, err := http.DefaultClient.Do(req)
if err != nil {
return "", fmt.Errorf("download image %q: %w", imageRef, err)
}
defer resp.Body.Close()

if resp.StatusCode != http.StatusOK {
return "", fmt.Errorf("download image %q: HTTP %d", imageRef, resp.StatusCode)
}

if _, err := io.Copy(tmp, resp.Body); err != nil {
return "", fmt.Errorf("write image to disk: %w", err)
}
if err := tmp.Close(); err != nil {
return "", fmt.Errorf("flush image to disk: %w", err)
}

if err := os.Rename(tmpName, dest); err != nil {
return "", fmt.Errorf("install cached image: %w", err)
}

return dest, nil
}

// cachedImagePath returns the deterministic local path for a given URL.
// Format: <cacheDir>/<sha256-of-url>-<basename>.img
func cachedImagePath(cacheDir, rawURL string) string {
h := sha256.Sum256([]byte(rawURL))
hash := fmt.Sprintf("%x", h[:8]) // 16 hex chars is plenty for uniqueness

base := filepath.Base(rawURL)
// Strip query strings / fragments that may appear in the "base"
if i := strings.IndexAny(base, "?#"); i != -1 {
base = base[:i]
}
// Normalise extension so libvirt always gets a known suffix
u, _ := url.Parse(rawURL)
if ext := filepath.Ext(u.Path); ext == "" || ext == ".img" {
base = strings.TrimSuffix(base, filepath.Ext(base)) + ".qcow2"
}

return filepath.Join(cacheDir, hash+"-"+base)
}

func isURL(s string) bool {
return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}
