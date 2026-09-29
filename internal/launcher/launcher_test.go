package launcher

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kratochj/stitkovac-gateway/internal/update"
)

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func healthy(version, marker string) string {
	return "#!/bin/sh\nprintf 'READY " + version + "\\n' >&3\nexec 3>&-\nIFS= read -r decision <&4\n[ \"$decision\" = CONTINUE ] || exit 12\nprintf '%s' " + quote(version) + " > " + quote(marker) + "\nwhile :; do sleep 1; done\n"
}
func setup(t *testing.T) (*update.Store, Options, func(string, string)) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	store, err := update.Open(root, map[string]ed25519.PublicKey{"test": pub}, "test-platform")
	if err != nil {
		t.Fatal(err)
	}
	stage := func(version, script string) {
		t.Helper()
		data := []byte(script)
		hash := sha256.Sum256(data)
		payload, _ := json.Marshal(update.Manifest{Schema: 1, Version: version, Platform: "test-platform", Size: int64(len(data)), SHA256: hex.EncodeToString(hash[:]), LauncherProtocol: update.LauncherProtocol})
		b, _ := json.Marshal(update.Envelope{KeyID: "test", Payload: payload, Signature: ed25519.Sign(key, append([]byte("stitkovac-gateway-release-v1\x00"), payload...))})
		if _, err := store.Stage(b, strings.NewReader(script)); err != nil {
			t.Fatal(err)
		}
	}
	return store, Options{Root: root, ReadyTimeout: 2 * time.Second, StopTimeout: 100 * time.Millisecond, Stdout: io.Discard, Stderr: io.Discard}, stage
}
func waitMarker(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err == nil && string(b) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("agent did not receive permission to run")
}
func start(t *testing.T, store *update.Store, o Options) (context.Context, context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- Run(ctx, store, o) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-result:
		case <-time.After(3 * time.Second):
			t.Error("launcher did not stop")
		}
	})
	return ctx, cancel, result
}
func TestFailedTrialRollsBackBeforePrinting(t *testing.T) {
	for _, bad := range []string{"#!/bin/sh\nexit 42\n", "#!/bin/sh\nsleep 60\n", "#!/bin/sh\nprintf 'READY 9.9.9\\n' >&3\nexec 3>&-\nsleep 60\n"} {
		t.Run(strings.TrimSpace(strings.Split(bad, "\n")[1]), func(t *testing.T) {
			store, o, stage := setup(t)
			marker := filepath.Join(o.Root, "running")
			stage("1.0.0", healthy("1.0.0", marker))
			stage("2.0.0", bad)
			if err := store.Initialize("1.0.0"); err != nil {
				t.Fatal(err)
			}
			if err := store.Request("2.0.0"); err != nil {
				t.Fatal(err)
			}
			start(t, store, o)
			waitMarker(t, marker, "1.0.0")
			s, err := store.Status()
			if err != nil {
				t.Fatal(err)
			}
			if s.Active != "1.0.0" || s.Trial || s.Failed != "2.0.0" {
				t.Fatalf("did not recover: %+v", s)
			}
		})
	}
}
func TestHealthyTrialIsConfirmedBeforeCloudWork(t *testing.T) {
	store, o, stage := setup(t)
	marker := filepath.Join(o.Root, "running")
	stage("1.0.0", healthy("1.0.0", marker))
	stage("2.0.0", healthy("2.0.0", marker))
	if err := store.Initialize("1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := store.Request("2.0.0"); err != nil {
		t.Fatal(err)
	}
	ctx, _, _ := start(t, store, o)
	waitMarker(t, marker, "2.0.0")
	s, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if s.Active != "2.0.0" || s.Trial || s.Previous != "1.0.0" {
		t.Fatalf("printing allowed without confirmation: %+v", s)
	}
	if err := Run(ctx, store, o); err == nil {
		t.Fatal("started second supervisor")
	}
}
func TestFailedStableAgentDoesNotInventRollback(t *testing.T) {
	store, o, stage := setup(t)
	stage("1.0.0", "#!/bin/sh\nexit 1\n")
	if err := store.Initialize("1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), store, o); err == nil {
		t.Fatal("ignored failed stable agent")
	}
	s, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if s.Active != "1.0.0" || s.Trial {
		t.Fatal("changed stable selection")
	}
}
