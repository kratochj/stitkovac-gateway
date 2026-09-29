package update

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type fixture struct {
	store *Store
	key   ed25519.PrivateKey
	root  string
}

func setup(t *testing.T) fixture {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := Open(root, map[string]ed25519.PublicKey{"test": pub}, "linux-arm64")
	if err != nil {
		t.Fatal(err)
	}
	return fixture{s, key, root}
}
func (f fixture) envelope(t *testing.T, version string, data []byte) []byte {
	t.Helper()
	hash := sha256.Sum256(data)
	return f.sign(t, Manifest{1, version, "linux-arm64", int64(len(data)), hex.EncodeToString(hash[:]), LauncherProtocol})
}
func (f fixture) sign(t *testing.T, m Manifest) []byte {
	t.Helper()
	payload, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(Envelope{"test", payload, ed25519.Sign(f.key, append([]byte(signatureDomain), payload...))})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func (f fixture) stage(t *testing.T, version string) {
	t.Helper()
	data := []byte("executable " + version)
	if _, err := f.store.Stage(f.envelope(t, version, data), bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestSignedArtifactValidation(t *testing.T) {
	for _, kind := range []string{"bad signature", "unknown key", "wrong architecture", "new launcher", "path traversal", "oversize", "wrong checksum", "truncated", "extra byte", "unknown field"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			data := []byte("binary")
			b := f.envelope(t, "1.0.0", data)
			var e Envelope
			must(t, json.Unmarshal(b, &e))
			var m Manifest
			must(t, json.Unmarshal(e.Payload, &m))
			switch kind {
			case "bad signature":
				e.Signature[0] ^= 1
				b, _ = json.Marshal(e)
			case "unknown key":
				e.KeyID = "unknown"
				b, _ = json.Marshal(e)
			case "wrong architecture":
				m.Platform = "linux-amd64"
				b = f.sign(t, m)
			case "new launcher":
				m.LauncherProtocol++
				b = f.sign(t, m)
			case "path traversal":
				m.Version = "../1.0.0"
				b = f.sign(t, m)
			case "oversize":
				m.Size = MaxArtifact + 1
				b = f.sign(t, m)
			case "wrong checksum":
				data = []byte("binarz")
			case "truncated":
				data = data[:len(data)-1]
			case "extra byte":
				data = append(data, 0)
			case "unknown field":
				b = append(b[:len(b)-1], []byte(`,"url":"https://untrusted.example"}`)...)
			}
			if _, err := f.store.Stage(b, bytes.NewReader(data)); err == nil {
				t.Fatal("accepted invalid release")
			}
			if _, err := os.Lstat(filepath.Join(f.root, "1.0.0")); !os.IsNotExist(err) {
				t.Fatal("published rejected release")
			}
		})
	}
}

func TestTrialCrashRecoveryPreservesApplicationData(t *testing.T) {
	f := setup(t)
	f.stage(t, "1.0.0")
	f.stage(t, "1.1.0")
	// This sentinel represents current journal data, not a release snapshot.
	dataPath := filepath.Join(f.root, "print-journal")
	must(t, os.WriteFile(dataPath, []byte("new print result"), 0600))
	must(t, f.store.Initialize("1.0.0"))
	must(t, f.store.Request("1.1.0"))
	s, err := f.store.Status()
	must(t, err)
	if s.Active != "1.0.0" || s.Pending != "1.1.0" {
		t.Fatalf("request interrupted active release: %+v", s)
	}
	s, err = f.store.BeginBoot()
	must(t, err)
	if !s.Trial || s.Active != "1.1.0" || s.Previous != "1.0.0" {
		t.Fatalf("wrong trial: %+v", s)
	}
	// Reopen without confirmation, as after losing power or killing the launcher.
	reopened, err := Open(f.root, f.store.keys, "linux-arm64")
	must(t, err)
	s, err = reopened.BeginBoot()
	must(t, err)
	if s.Trial || s.Active != "1.0.0" || s.Failed != "1.1.0" {
		t.Fatalf("no rollback: %+v", s)
	}
	b, err := os.ReadFile(dataPath)
	must(t, err)
	if string(b) != "new print result" {
		t.Fatal("journal was replaced")
	}
	s, err = reopened.BeginBoot()
	must(t, err)
	if s.Active != "1.0.0" {
		t.Fatal("retried failed release")
	}
}

func TestConfirmationAndDowngradeProtection(t *testing.T) {
	f := setup(t)
	f.stage(t, "1.9.0")
	f.stage(t, "1.10.0")
	must(t, f.store.Initialize("1.9.0"))
	must(t, f.store.Request("1.10.0"))
	must(t, f.store.Request("1.10.0"))
	_, err := f.store.BeginBoot()
	must(t, err)
	if err := f.store.Confirm("1.9.0"); err == nil {
		t.Fatal("confirmed another process version")
	}
	must(t, f.store.Confirm("1.10.0"))
	s, err := f.store.BeginBoot()
	must(t, err)
	if s.Trial || s.Active != "1.10.0" {
		t.Fatalf("confirmed release rolled back: %+v", s)
	}
	if err := f.store.Request("1.9.0"); err == nil {
		t.Fatal("accepted downgrade")
	}
	if err := f.store.Initialize("1.9.0"); err == nil {
		t.Fatal("overwrote initialized state")
	}
}

func TestCorruptCandidateKeepsPreviousVersion(t *testing.T) {
	f := setup(t)
	f.stage(t, "1.0.0")
	f.stage(t, "2.0.0")
	must(t, f.store.Initialize("1.0.0"))
	must(t, f.store.Request("2.0.0"))
	path := filepath.Join(f.root, "2.0.0", "gateway")
	must(t, os.Chmod(path, 0700))
	must(t, os.WriteFile(path, []byte("corrupt"), 0500))
	must(t, os.Chmod(path, 0500))
	s, err := f.store.BeginBoot()
	must(t, err)
	if s.Active != "1.0.0" || s.Trial || s.Failed != "2.0.0" {
		t.Fatalf("did not discard corrupt candidate: %+v", s)
	}
}

func TestPartialInstallAndSymlinksAreNotExecutable(t *testing.T) {
	f := setup(t)
	f.stage(t, "1.0.0")
	must(t, f.store.Initialize("1.0.0"))
	must(t, os.Mkdir(filepath.Join(f.root, ".staging-interrupted"), 0700))
	must(t, os.Symlink(filepath.Join(f.root, "1.0.0"), filepath.Join(f.root, "2.0.0")))
	if _, err := f.store.Executable("2.0.0"); err == nil {
		t.Fatal("followed symlink")
	}
	s, err := f.store.BeginBoot()
	must(t, err)
	if s.Active != "1.0.0" {
		t.Fatal("activated partial release")
	}
	f.stage(t, "3.0.0")
	path := filepath.Join(f.root, "3.0.0", "gateway")
	must(t, os.Remove(path))
	must(t, os.Symlink(filepath.Join(f.root, "1.0.0", "gateway"), path))
	if _, err := f.store.Executable("3.0.0"); err == nil {
		t.Fatal("followed executable symlink")
	}
}

func TestSelectionCorruptionFailsClosed(t *testing.T) {
	f := setup(t)
	f.stage(t, "1.0.0")
	must(t, f.store.Initialize("1.0.0"))
	must(t, os.WriteFile(filepath.Join(f.root, "selection.json"), []byte(`{"active":"../gateway","trial":true}`), 0600))
	if _, err := f.store.BeginBoot(); err == nil {
		t.Fatal("accepted invalid selection")
	}
}

func TestExistingReleaseCannotBeOverwritten(t *testing.T) {
	f := setup(t)
	f.stage(t, "1.0.0")
	before, err := os.ReadFile(filepath.Join(f.root, "1.0.0", "gateway"))
	must(t, err)
	data := []byte("replacement")
	if _, err := f.store.Stage(f.envelope(t, "1.0.0", data), bytes.NewReader(data)); err == nil {
		t.Fatal("overwrote immutable release")
	}
	after, err := os.ReadFile(filepath.Join(f.root, "1.0.0", "gateway"))
	must(t, err)
	if !bytes.Equal(before, after) {
		t.Fatal("existing release changed")
	}
}
