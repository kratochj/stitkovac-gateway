package cloud

import (
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

var ErrAddress = errors.New("invalid server address")
var ErrToken = errors.New("invalid gateway token")
var ErrNewTokenRequired = errors.New("new token required")
var ErrConfigStorage = errors.New("configuration could not be saved")

// Config is private persistent state, never a template or diagnostic payload.
type Config struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

func NormalizeConfig(base, token string) (Config, error) {
	base, token = strings.TrimSpace(base), strings.TrimSpace(token)
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" || len(base) > 2048 {
		return Config{}, ErrAddress
	}
	if len(token) < 32 || len(token) > 4096 {
		return Config{}, ErrToken
	}
	for _, c := range token {
		if c < 33 || c > 126 {
			return Config{}, ErrToken
		}
	}
	u.Path = ""
	u.Host = strings.ToLower(u.Host)
	return Config{URL: u.String(), Token: token}, nil
}

func LoadConfig(dir string) (Config, bool, error) {
	path := filepath.Join(dir, "cloud.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, false, nil
	}
	if err != nil {
		return Config{}, false, errors.New("cloud configuration unavailable")
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return Config{}, false, errors.New("cloud configuration must be a private regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return Config{}, false, errors.New("cloud configuration unavailable")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil || len(b) > 8192 {
		return Config{}, false, errors.New("invalid cloud configuration")
	}
	var cfg Config
	if json.Unmarshal(b, &cfg) != nil {
		return Config{}, false, errors.New("invalid cloud configuration")
	}
	cfg, err = NormalizeConfig(cfg.URL, cfg.Token)
	return cfg, true, err
}
