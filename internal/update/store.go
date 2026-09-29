package update

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kratochj/stitkovac-gateway/internal/platform"
)

type Store struct {
	root     string
	keys     map[string]ed25519.PublicKey
	platform string
}

// Selection is a small atomic journal. It never includes or restores print data.
type Selection struct {
	Active          string `json:"active"`
	Previous        string `json:"previous,omitempty"`
	Pending         string `json:"pending,omitempty"`
	Trial           bool   `json:"trial"`
	Failed          string `json:"failed,omitempty"`
	PendingRollback bool   `json:"pendingRollback,omitempty"`
}

// Open requires an explicitly provisioned private directory on persistent storage.
func Open(root string, keys map[string]ed25519.PublicKey, target string) (*Store, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("release directory must be private and not a symlink")
	}
	if len(keys) == 0 {
		return nil, errors.New("trusted release keys are required")
	}
	copyKeys := map[string]ed25519.PublicKey{}
	for id, key := range keys {
		if !keyPattern.MatchString(id) || len(key) != ed25519.PublicKeySize {
			return nil, errors.New("invalid trusted release key")
		}
		copyKeys[id] = append(ed25519.PublicKey(nil), key...)
	}
	return &Store{root: root, keys: copyKeys, platform: target}, nil
}

func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func readFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("invalid release file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, errors.New("release file exceeds limit")
	}
	return b, err
}

func writeSynced(path string, src io.Reader, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, src); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return f.Close()
}

// Stage publishes a complete immutable release directory only after signature,
// length and digest verification and fsync. Partial staging is never executable.
func (s *Store) Stage(envelope []byte, artifact io.Reader) (Manifest, error) {
	m, err := Verify(envelope, s.keys, s.platform)
	if err != nil {
		return Manifest{}, err
	}
	unlock, err := platform.Lock(s.root)
	if err != nil {
		return Manifest{}, err
	}
	defer unlock()
	final := filepath.Join(s.root, m.Version)
	if _, err := os.Lstat(final); !errors.Is(err, os.ErrNotExist) {
		return Manifest{}, errors.New("release already exists or cannot be inspected")
	}
	if err := checkSpace(s.root, m.Size); err != nil {
		return Manifest{}, err
	}
	temp, err := os.MkdirTemp(s.root, ".staging-")
	if err != nil {
		return Manifest{}, err
	}
	defer os.RemoveAll(temp)
	hash := sha256.New()
	reader := io.TeeReader(io.LimitReader(artifact, m.Size+1), hash)
	if err := writeSynced(filepath.Join(temp, "gateway"), reader, 0500); err != nil {
		return Manifest{}, err
	}
	info, err := os.Stat(filepath.Join(temp, "gateway"))
	if err != nil {
		return Manifest{}, err
	}
	if info.Size() != m.Size || hex.EncodeToString(hash.Sum(nil)) != m.SHA256 {
		return Manifest{}, errors.New("release artifact integrity check failed")
	}
	if err := platform.AtomicWrite(filepath.Join(temp, "manifest.json"), envelope, 0400); err != nil {
		return Manifest{}, err
	}
	if err := syncDir(temp); err != nil {
		return Manifest{}, err
	}
	if err := os.Rename(temp, final); err != nil {
		return Manifest{}, err
	}
	if err := syncDir(s.root); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// Executable rechecks the signature and binary before every process start.
func (s *Store) Executable(version string) (string, error) {
	if !versionPattern.MatchString(version) {
		return "", errors.New("invalid release version")
	}
	dir := filepath.Join(s.root, version)
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return "", errors.New("invalid release directory")
	}
	envelope, err := readFile(filepath.Join(dir, "manifest.json"), MaxManifest)
	if err != nil {
		return "", err
	}
	m, err := Verify(envelope, s.keys, s.platform)
	if err != nil {
		return "", err
	}
	if m.Version != version {
		return "", errors.New("release directory version mismatch")
	}
	path := filepath.Join(dir, "gateway")
	info, err = os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0500 || info.Size() != m.Size {
		return "", errors.New("invalid release executable")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(f, m.Size+1))
	if err != nil {
		return "", err
	}
	if n != m.Size || hex.EncodeToString(hash.Sum(nil)) != m.SHA256 {
		return "", errors.New("release executable checksum mismatch")
	}
	return path, nil
}

func (s *Store) readSelection() (Selection, error) {
	b, err := readFile(filepath.Join(s.root, "selection.json"), 4096)
	if err != nil {
		return Selection{}, err
	}
	var v Selection
	if strictJSON(b, &v) != nil || !versionPattern.MatchString(v.Active) ||
		v.Previous != "" && (!versionPattern.MatchString(v.Previous) || v.Previous == v.Active) ||
		v.Pending != "" && (!versionPattern.MatchString(v.Pending) || (!newer(v.Pending, v.Active) && !v.PendingRollback)) ||
		v.PendingRollback && (v.Pending == "" || v.Pending != v.Previous) ||
		v.Failed != "" && !versionPattern.MatchString(v.Failed) ||
		v.Trial && (v.Previous == "" || v.Pending != "") {
		return Selection{}, errors.New("invalid release selection; operator recovery required")
	}
	return v, nil
}

func (s *Store) save(v Selection) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return platform.AtomicWrite(filepath.Join(s.root, "selection.json"), b, 0600)
}

func (s *Store) Initialize(version string) error {
	unlock, err := platform.Lock(s.root)
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := os.Lstat(filepath.Join(s.root, "selection.json")); !errors.Is(err, os.ErrNotExist) {
		return errors.New("release selection already exists or cannot be inspected")
	}
	if _, err := s.Executable(version); err != nil {
		return err
	}
	return s.save(Selection{Active: version})
}

func (s *Store) Status() (Selection, error) { return s.readSelection() }

// Request only queues a verified newer version. The running application is not
// interrupted; the supervisor must drain printing before its next BeginBoot.
func (s *Store) Request(version string) error {
	unlock, err := platform.Lock(s.root)
	if err != nil {
		return err
	}
	defer unlock()
	v, err := s.readSelection()
	if err != nil {
		return err
	}
	if _, err := s.Executable(version); err != nil {
		return err
	}
	if v.Trial || !newer(version, v.Active) || v.Pending != "" && v.Pending != version {
		return errors.New("release is not a new unambiguous update")
	}
	v.Pending, v.Failed, v.PendingRollback = version, "", false
	return s.save(v)
}

// BeginBoot durably marks the trial before executing it. A subsequent invocation
// after power loss rolls back an unconfirmed trial without touching application DBs.
// Exactly one supervisor may call this; it holds a separate lifetime process lock.
func (s *Store) BeginBoot() (Selection, error) {
	unlock, err := platform.Lock(s.root)
	if err != nil {
		return Selection{}, err
	}
	defer unlock()
	v, err := s.readSelection()
	if err != nil {
		return Selection{}, err
	}
	if v.Trial {
		v.Active, v.Previous, v.Trial, v.Failed = v.Previous, "", false, v.Active
	} else if v.Pending != "" {
		if _, err := s.Executable(v.Pending); err != nil {
			v.Failed, v.Pending = v.Pending, ""
		} else {
			v.Active, v.Previous, v.Pending, v.Trial = v.Pending, v.Active, "", true
		}
	}
	v.PendingRollback = false
	// Validate both the candidate and its fallback before changing the selection.
	if _, err := s.Executable(v.Active); err != nil {
		return Selection{}, err
	}
	if v.Trial {
		if _, err := s.Executable(v.Previous); err != nil {
			return Selection{}, err
		}
	}
	if err := s.save(v); err != nil {
		return Selection{}, err
	}
	return v, nil
}

func (s *Store) Confirm(version string) error {
	unlock, err := platform.Lock(s.root)
	if err != nil {
		return err
	}
	defer unlock()
	v, err := s.readSelection()
	if err != nil {
		return err
	}
	if v.Active != version || !v.Trial {
		return errors.New("no matching trial to confirm")
	}
	v.Trial = false
	return s.save(v)
}

// RequestRollback is deliberately separate from normal updates and can only
// select the retained, verified previous release, never an arbitrary downgrade.
func (s *Store) RequestRollback(version string) error {
	unlock, err := platform.Lock(s.root)
	if err != nil {
		return err
	}
	defer unlock()
	v, err := s.readSelection()
	if err != nil {
		return err
	}
	if v.Trial || v.Pending != "" || version != v.Previous || version == "" {
		return errors.New("no matching previous release")
	}
	if _, err := s.Executable(version); err != nil {
		return err
	}
	v.Pending = version
	v.PendingRollback = true
	v.Failed = ""
	return s.save(v)
}

// Cleanup retains the active, previous, pending and explicitly staged candidate.
// It shares the installer lock; no partial download can be removed while written.
func (s *Store) Cleanup(candidate string) error {
	unlock, err := platform.Lock(s.root)
	if err != nil {
		return err
	}
	defer unlock()
	v, err := s.readSelection()
	if err != nil {
		return err
	}
	keep := map[string]bool{v.Active: true, v.Previous: true, v.Pending: true, candidate: true}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if keep[e.Name()] || (!versionPattern.MatchString(e.Name()) && !strings.HasPrefix(e.Name(), ".staging-")) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(s.root, e.Name())); err != nil {
			return err
		}
	}
	return syncDir(s.root)
}
