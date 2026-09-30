package network

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const SocketPath = "/run/stitkovac-gateway-network/control.sock"

type peerKey struct{}

func (c *Controller) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, ok := r.Context().Value(peerKey{}).(uint32)
		if !ok || (uid != c.cfg.AgentUID && uid != 0) {
			http.Error(w, "forbidden", 403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		var value any
		var err error
		switch r.Method + " " + r.URL.Path {
		case "GET /status":
			value = c.Status()
		case "GET /scan":
			value, err = c.Scan(r.Context())
		case "POST /wifi":
			var req WiFiRequest
			d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
			d.DisallowUnknownFields()
			if d.Decode(&req) != nil || d.Decode(&struct{}{}) != io.EOF {
				err = ErrInvalid
			} else {
				err = c.Apply(req)
			}
		case "POST /wifi-admin":
			var req struct {
				Enabled *bool `json:"enabled"`
			}
			d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128))
			d.DisallowUnknownFields()
			if d.Decode(&req) != nil || d.Decode(&struct{}{}) != io.EOF || req.Enabled == nil {
				err = ErrInvalid
			} else {
				err = c.SetWiFiAdmin(*req.Enabled)
			}
		case "POST /ap":
			err = c.ServiceAP(true)
		case "POST /uplink":
			err = c.ServiceAP(false)
		default:
			http.NotFound(w, r)
			return
		}
		if err != nil {
			code := http.StatusServiceUnavailable
			if errors.Is(err, ErrInvalid) {
				code = 400
			} else if errors.Is(err, ErrBusy) {
				code = 409
			}
			http.Error(w, http.StatusText(code), code)
			return
		}
		_ = json.NewEncoder(w).Encode(value)
	})
}
func (c *Controller) Serve(ctx context.Context, path string) error {
	// The parent is root-owned and not writable by the web account.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0022 != 0 {
		return ErrStorage
	}
	if err := os.Chown(dir, 0, c.cfg.AgentGID); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return ErrStorage
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err := os.Chown(path, 0, c.cfg.AgentGID); err != nil {
		return err
	}
	if err := os.Chmod(path, 0660); err != nil {
		return err
	}
	server := &http.Server{Handler: c.handler(), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 12 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 4096, ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
		uid, err := peerUID(conn)
		if err != nil {
			return ctx
		}
		return context.WithValue(ctx, peerKey{}, uid)
	}}
	go func() { <-ctx.Done(); _ = server.Close() }()
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type Client struct{ http *http.Client }

func NewClient(path string) *Client {
	return &Client{http: &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) request(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			return ErrInvalid
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://network.local"+path, body)
	if err != nil {
		return ErrInvalid
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		switch resp.StatusCode {
		case 400:
			return ErrInvalid
		case 409:
			return ErrBusy
		default:
			return ErrUnavailable
		}
	}
	if output != nil {
		if json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(output) != nil {
			return ErrUnavailable
		}
	}
	return nil
}
func (c *Client) Status(ctx context.Context) (Status, error) {
	var s Status
	err := c.request(ctx, "GET", "/status", nil, &s)
	return s, err
}
func (c *Client) Scan(ctx context.Context) ([]AccessPoint, error) {
	var a []AccessPoint
	err := c.request(ctx, "GET", "/scan", nil, &a)
	return a, err
}
func (c *Client) Apply(ctx context.Context, r WiFiRequest) error {
	return c.request(ctx, "POST", "/wifi", r, nil)
}
func (c *Client) ServiceAP(ctx context.Context, enable bool) error {
	path := "/uplink"
	if enable {
		path = "/ap"
	}
	return c.request(ctx, "POST", path, nil, nil)
}

func (c *Client) SetWiFiAdmin(ctx context.Context, enabled bool) error {
	return c.request(ctx, "POST", "/wifi-admin", struct {
		Enabled bool `json:"enabled"`
	}{enabled}, nil)
}
