package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDownloadVerifiesBeforePublishing(t *testing.T) {
	f := setup(t)
	data := []byte("release executable")
	manifest := f.envelope(t, "1.0.0", data)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("sent gateway credentials to release host")
		}
		switch r.URL.Path {
		case "/releases/1.0.0/linux-arm64/manifest.json":
			w.Write(manifest)
		case "/releases/1.0.0/linux-arm64/gateway":
			w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	m, err := f.store.Download(context.Background(), server.URL, "1.0.0", server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "1.0.0" {
		t.Fatal("wrong release")
	}
	if _, err := f.store.Executable("1.0.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Status(); !os.IsNotExist(err) {
		t.Fatal("download implicitly selected a release")
	}
}

func TestDownloadRejectsRepositoryManipulation(t *testing.T) {
	for _, kind := range []string{"redirect", "another version", "invalid signature", "oversize manifest", "corrupt executable", "extra executable bytes", "truncated executable"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			data := []byte("binary")
			manifest := f.envelope(t, "1.0.0", data)
			var artifactRequests atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "manifest.json") {
					switch kind {
					case "redirect":
						http.Redirect(w, r, "https://example.invalid/manifest.json", http.StatusFound)
					case "another version":
						w.Write(f.envelope(t, "2.0.0", data))
					case "invalid signature":
						w.Write([]byte(`{"keyId":"unknown"}`))
					case "oversize manifest":
						w.Write([]byte(strings.Repeat(" ", MaxManifest+1)))
					default:
						w.Write(manifest)
					}
				} else {
					artifactRequests.Add(1)
					switch kind {
					case "corrupt executable":
						w.Write([]byte("binarz"))
					case "extra executable bytes":
						w.Write(append(data, 0))
					case "truncated executable":
						w.Write(data[:len(data)-1])
					default:
						t.Error("downloaded artifact before validating manifest")
						http.Error(w, "unexpected", 500)
					}
				}
			}))
			defer server.Close()
			if _, err := f.store.Download(context.Background(), server.URL, "1.0.0", server.Client().Transport); err == nil {
				t.Fatal("accepted invalid release")
			}
			if _, err := os.Lstat(filepath.Join(f.root, "1.0.0")); !os.IsNotExist(err) {
				t.Fatal("published invalid download")
			}
			if (kind == "redirect" || kind == "another version" || kind == "invalid signature" || kind == "oversize manifest") && artifactRequests.Load() != 0 {
				t.Fatal("fetched unverified artifact")
			}
		})
	}
}

func TestDownloadRestrictsOriginAndVersion(t *testing.T) {
	f := setup(t)
	for _, origin := range []string{"http://example.com", "https://user:password@example.com", "https://example.com/path", "https://example.com?x=1", "https://example.com#other"} {
		if _, err := f.store.Download(context.Background(), origin, "1.0.0", nil); err == nil {
			t.Fatal("accepted invalid origin")
		}
	}
	if _, err := f.store.Download(context.Background(), "https://example.com", "../escape", nil); err == nil {
		t.Fatal("accepted traversal")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.store.Download(ctx, "https://example.com", "1.0.0", nil); err == nil {
		t.Fatal("ignored cancelled update")
	}
}

func TestDownloadWaitsForHostingCapacity(t *testing.T) {
	f := setup(t)
	data := []byte("signed artifact")
	envelope := f.envelope(t, "1.0.0", data)
	var attempts atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if strings.HasSuffix(r.URL.Path, "manifest.json") {
			w.Write(envelope)
		} else {
			w.Write(data)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := f.store.Download(ctx, server.URL, "1.0.0", server.Client().Transport)
	must(t, err)
	if attempts.Load() != 4 {
		t.Fatal("download did not retry hosting pressure")
	}
	_, err = f.store.Executable("1.0.0")
	must(t, err)
}
func TestHostingBackoffHonorsCancellation(t *testing.T) {
	f := setup(t)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Retry-After", "30"); w.WriteHeader(503) }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := f.store.Download(ctx, server.URL, "1.0.0", server.Client().Transport); err == nil {
		t.Fatal("unexpected success")
	}
	if time.Since(start) > time.Second {
		t.Fatal("backoff ignored cancellation")
	}
}
