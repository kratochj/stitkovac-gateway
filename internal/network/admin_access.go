package network

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/kratochj/stitkovac-gateway/internal/platform"
)

type adminAccess struct {
	Enabled bool `json:"enabled"`
}

func (c *Controller) loadWiFiAdmin() error {
	path := filepath.Join(c.cfg.DataDir, "admin-access.json")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 128 {
		return ErrStorage
	}
	b, err := os.ReadFile(path)
	var access adminAccess
	if err != nil || json.Unmarshal(b, &access) != nil {
		return ErrStorage
	}
	c.wifiAdmin = access.Enabled
	return nil
}

// Keep this setting separate from in-flight Wi-Fi selection snapshots.
func (c *Controller) SetWiFiAdmin(enabled bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fatal {
		return ErrStorage
	}
	b, _ := json.Marshal(adminAccess{Enabled: enabled})
	if platform.AtomicWrite(filepath.Join(c.cfg.DataDir, "admin-access.json"), b, 0600) != nil {
		return ErrStorage
	}
	c.wifiAdmin = enabled
	return nil
}
