package cloud

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kratochj/stitkovac-gateway/internal/telemetry"
)

func TestConfigurationPersistenceAndCredentialBoundary(t *testing.T) {
	dir := t.TempDir()
	token := strings.Repeat("secret", 8)
	manager := NewManager(dir, Config{}, nil)
	if err := manager.Save("https://CLOUD.example/", token); err != nil {
		t.Fatal(err)
	}
	cfg, exists, err := LoadConfig(dir)
	if err != nil || !exists || cfg.URL != "https://cloud.example" || cfg.Token != token {
		t.Fatal("configuration was not persisted correctly")
	}
	info, _ := os.Stat(filepath.Join(dir, "cloud.json"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("credential file is not private")
	}
	for _, input := range []struct{ url, token string }{
		{"https://other.example", ""}, {"http://cloud.example", token},
		{"https://cloud.example/api", token}, {"https://user:pass@cloud.example", token},
		{"https://cloud.example?token=secret", token}, {"https://cloud.example", "short"},
		{"https://cloud.example", strings.Repeat("x", 32) + "\nHeader: injected"},
	} {
		if manager.Save(input.url, input.token) == nil {
			t.Fatal("invalid configuration accepted")
		}
		current, _, _ := LoadConfig(dir)
		if current != cfg {
			t.Fatal("rejected input changed stored credentials")
		}
	}
	if err := manager.Save("https://cloud.example/", ""); err != nil {
		t.Fatal(err)
	}
	newToken := strings.Repeat("replacement", 4)
	if err := manager.Save("https://other.example", newToken); err != nil {
		t.Fatal(err)
	}
	restored, _, err := LoadConfig(dir)
	if err != nil || restored.URL != "https://other.example" || restored.Token != newToken {
		t.Fatal("replacement did not survive reload")
	}
	if err := os.Chmod(filepath.Join(dir, "cloud.json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadConfig(dir); err == nil {
		t.Fatal("accepted readable credentials")
	}
}

type managedTestConnection struct {
	cfg       Config
	started   chan Config
	cancelled chan struct{}
	drained   chan struct{}
	observe   func(string)
}

func (c *managedTestConnection) Run(ctx context.Context) {
	c.started <- c.cfg
	c.observe("connected")
	<-ctx.Done()
	close(c.cancelled)
	<-c.drained
}
func (c *managedTestConnection) Report(context.Context, telemetry.Event) error { return nil }

func TestReconfigurationWaitsForPreviousWorkerToDrain(t *testing.T) {
	dir := t.TempDir()
	first := Config{URL: "https://first.example", Token: strings.Repeat("a", 32)}
	started := make(chan Config, 4)
	cancelled := make(chan struct{})
	drain := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := NewManager(dir, first, func(cfg Config, observe func(string)) (Connection, error) {
		c := &managedTestConnection{cfg: cfg, started: started, observe: observe, cancelled: make(chan struct{}), drained: make(chan struct{})}
		if cfg == first {
			c.cancelled, c.drained = cancelled, drain
		} else {
			close(c.drained)
		}
		return c, nil
	})
	done := make(chan struct{})
	go func() { defer close(done); manager.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("manager did not stop")
		}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first connection did not start")
	}
	if err := manager.Save("https://second.example", strings.Repeat("b", 32)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("old connection was not stopped")
	}
	select {
	case <-started:
		t.Fatal("new connection overlapped an unfinished local write")
	default:
	}
	// An additional save while draining must win over the intermediate configuration.
	if err := manager.Save("https://third.example", strings.Repeat("c", 32)); err != nil {
		t.Fatal(err)
	}
	close(drain)
	select {
	case cfg := <-started:
		if cfg.URL != "https://third.example" {
			t.Fatal("did not apply latest configuration")
		}
	case <-time.After(time.Second):
		t.Fatal("replacement did not start")
	}
}

func TestFailedSaveDoesNotReplaceRuntimeConfiguration(t *testing.T) {
	cfg := Config{URL: "https://original.example", Token: strings.Repeat("s", 32)}
	manager := NewManager(filepath.Join(t.TempDir(), "absent"), cfg, nil)
	before := manager.Status()
	if err := manager.Save("https://new.example", strings.Repeat("n", 32)); err == nil {
		t.Fatal("missing data storage accepted")
	}
	if manager.Status() != before {
		t.Fatal("failed persistence changed active settings")
	}
}
