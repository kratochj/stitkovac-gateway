// Package cloud implements the proposed v1 gateway contract. Production server
// support must be deployed before configuring a gateway to use this transport.
package cloud

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/kratochj/stitkovac-gateway/internal/ota"
	"github.com/kratochj/stitkovac-gateway/internal/printing"
	"github.com/kratochj/stitkovac-gateway/internal/state"
	"github.com/kratochj/stitkovac-gateway/internal/telemetry"
)

var errUnauthorized = errors.New("cloud authentication rejected")
var errHandshake = errors.New("cloud handshake rejected")

var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type Client struct {
	base, token, id, version string
	http                     *http.Client
	worker                   *printing.Worker
	report                   func(string)
	OnState                  func(string)
	OnPanic                  func(telemetry.Diagnostic)
	OnOTA                    func(ota.Remote)
}
type envelope struct {
	Version           int    `json:"version"`
	Type              string `json:"type"`
	MessageID         string `json:"messageId"`
	SessionID         string `json:"sessionId,omitempty"`
	GatewayID         string `json:"gatewayId,omitempty"`
	AgentVersion      string `json:"agentVersion,omitempty"`
	InventoryProtocol int    `json:"inventoryProtocol,omitempty"`
	OTAProtocol       int    `json:"otaProtocol,omitempty"`
}
type page struct {
	Jobs       []string `json:"jobs"`
	NextCursor string   `json:"nextCursor"`
}
type sessionCloud struct {
	client  *Client
	session string
}

func New(base, token, id, version string, worker *printing.Worker, transport http.RoundTripper, report func(string)) (*Client, error) {
	cfg, err := NormalizeConfig(base, token)
	if err != nil {
		return nil, err
	}
	h := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("cloud redirects are forbidden") }}
	return &Client{base: cfg.URL, token: cfg.Token, id: id, version: version, http: h, worker: worker, report: report}, nil
}

func (c *Client) request(ctx context.Context, method, path, session string, body any, out any) error {
	var b []byte
	var err error
	if body != nil {
		b, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	r, err := http.NewRequestWithContext(ctx, method, c.base+"/api/gateway/v1"+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	r.Header.Set("User-Agent", "Stitkovac-Gateway/"+c.version)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if session != "" {
		r.Header.Set("X-Gateway-Session", session)
	}
	resp, err := c.http.Do(r)
	if err != nil {
		return errors.New("cloud request failed")
	}
	defer resp.Body.Close()
	if out == nil {
		if resp.StatusCode != http.StatusNoContent {
			return fmt.Errorf("cloud rejected operation (%d)", resp.StatusCode)
		}
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cloud rejected request (%d)", resp.StatusCode)
	}
	if target, ok := out.(*[]byte); ok {
		*target, err = io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
		if err != nil {
			return err
		}
		if len(*target) > 8<<20 {
			return errors.New("document exceeds limit")
		}
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 256<<10)).Decode(out)
}

func (s sessionCloud) Start(ctx context.Context, a state.Attempt) error {
	return s.client.request(ctx, "POST", "/jobs/"+a.JobUID+"/start", s.session, map[string]string{"attemptId": a.AttemptID}, nil)
}
func (s sessionCloud) Result(ctx context.Context, a state.Attempt) error {
	return s.client.request(ctx, "POST", "/jobs/"+a.JobUID+"/result", s.session, map[string]string{"attemptId": a.AttemptID, "state": a.State, "reason": a.Reason}, nil)
}

func (c *Client) syncJobs(ctx context.Context, session string) error {
	cursor := ""
	seen := map[string]bool{}
	for {
		var p page
		if err := c.request(ctx, "GET", "/jobs?cursor="+url.QueryEscape(cursor), session, nil, &p); err != nil {
			return err
		}
		if len(p.Jobs) > 100 {
			return errors.New("cloud page exceeds limit")
		}
		// Preserve server ordering within each physical endpoint; independent printers
		// may finish concurrently without allowing a later page to overtake this one.
		groups := map[string][]state.Attempt{}
		totalBytes := 0
		for _, uid := range p.Jobs {
			if !safeID.MatchString(uid) {
				return errors.New("invalid cloud job identifier")
			}
			var a state.Attempt
			if err := c.request(ctx, "POST", "/jobs/"+uid+"/claim", session, struct{}{}, &a); err != nil {
				return err
			}
			if a.JobUID != uid || !safeID.MatchString(a.AttemptID) {
				return errors.New("invalid cloud claim")
			}
			if a.State == "STARTED" {
				if err := c.worker.Store.RememberRemoteStart(ctx, a); err != nil {
					return err
				}
				current, err := c.worker.Store.Attempt(ctx, a.JobUID, a.AttemptID)
				if err != nil {
					return err
				}
				if err = (sessionCloud{c, session}).Result(ctx, current); err != nil {
					return err
				}
				if err = c.worker.Store.Acknowledge(ctx, a.JobUID, a.AttemptID, current.State); err != nil {
					return err
				}
				continue
			}
			if a.State == "EXPIRED" {
				if err := c.worker.Store.ServerExpired(ctx, a.JobUID, a.AttemptID); err != nil {
					return err
				}
				continue
			}
			if err := c.request(ctx, "GET", "/jobs/"+uid+"/document?attemptId="+url.QueryEscape(a.AttemptID), session, nil, &a.Document); err != nil {
				return err
			}
			totalBytes += len(a.Document)
			if totalBytes > 16<<20 {
				return errors.New("cloud page documents exceed memory limit")
			}
			key := fmt.Sprintf("%s:%d", a.IP, a.Port)
			groups[key] = append(groups[key], a)
		}
		var wg sync.WaitGroup
		fail := make(chan error, len(groups))
		limit := make(chan struct{}, 4)
		for _, jobs := range groups {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer telemetry.Recover(c.OnPanic, func() { fail <- telemetry.ErrPanic })
				select {
				case limit <- struct{}{}:
					defer func() { <-limit }()
				case <-ctx.Done():
					fail <- ctx.Err()
					return
				}
				for _, a := range jobs {
					if err := c.worker.Process(ctx, a, sessionCloud{c, session}); err != nil {
						fail <- err
						return
					}
				}
			}()
		}
		wg.Wait()
		close(fail)
		for err := range fail {
			return err
		}
		if p.NextCursor == "" {
			return nil
		}
		if len(p.NextCursor) > 4096 || seen[p.NextCursor] {
			return errors.New("invalid cloud cursor")
		}
		seen[p.NextCursor] = true
		cursor = p.NextCursor
	}
}

// Session ends on any failed synchronization; the reconnect path reconciles the
// durable journal instead of polling the queue or repeating local writes.
func (c *Client) Session(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	wsHTTP := *c.http
	wsHTTP.Timeout = 0
	dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
	conn, response, err := websocket.Dial(dialCtx, c.base+"/api/gateway/v1/connect", &websocket.DialOptions{HTTPClient: &wsHTTP, HTTPHeader: http.Header{"Authorization": []string{"Bearer " + c.token}, "User-Agent": []string{"Stitkovac-Gateway/" + c.version}}})
	dialCancel()
	if err != nil {
		if response != nil && (response.StatusCode == 401 || response.StatusCode == 403) {
			return errUnauthorized
		}
		return errors.New("cloud websocket unavailable")
	}
	defer conn.CloseNow()
	conn.SetReadLimit(16 << 10)
	protocol := 0
	if c.OnOTA != nil {
		protocol = 1
	}
	hello, _ := json.Marshal(envelope{InventoryProtocol: 1, OTAProtocol: protocol, Version: 1, Type: "hello", MessageID: state.ID(), GatewayID: c.id, AgentVersion: c.version})
	if err := conn.Write(ctx, websocket.MessageText, hello); err != nil {
		return err
	}
	handshake, done := context.WithTimeout(ctx, 10*time.Second)
	kind, b, err := conn.Read(handshake)
	done()
	if err != nil {
		return errHandshake
	}
	var ready envelope
	if kind != websocket.MessageText || json.Unmarshal(b, &ready) != nil || ready.Version != 1 || ready.Type != "ready" || !safeID.MatchString(ready.SessionID) {
		return errHandshake
	}
	if c.OnState != nil {
		c.OnState("connected")
	}
	if c.OnOTA != nil {
		c.OnOTA(otaRemote{c})
	}
	wake := make(chan struct{}, 1)
	wake <- struct{}{}
	fail := make(chan error, 3)
	var workers sync.WaitGroup
	workers.Add(3)
	go func() {
		defer workers.Done()
		defer telemetry.Recover(c.OnPanic, func() { fail <- telemetry.ErrPanic })
		for {
			kind, b, err := conn.Read(ctx)
			if err != nil {
				fail <- err
				return
			}
			var e envelope
			if kind != websocket.MessageText || json.Unmarshal(b, &e) != nil || e.Version != 1 {
				fail <- errors.New("invalid gateway message")
				return
			}
			if e.Type == "ota.available" && c.OnOTA != nil {
				c.OnOTA(otaRemote{c})
			}
			if e.Type == "jobs.available" {
				select {
				case wake <- struct{}{}:
				default:
				}
			}
		}
	}()
	go func() {
		defer workers.Done()
		defer telemetry.Recover(c.OnPanic, func() { fail <- telemetry.ErrPanic })
		for {
			select {
			case <-ctx.Done():
				return
			case <-c.worker.Store.Changes():
				if ready.InventoryProtocol == 1 {
					select {
					case wake <- struct{}{}:
					default:
					}
				}
			case <-wake:
				if ready.InventoryProtocol == 1 {
					if err := c.syncOperations(ctx, ready.SessionID); err != nil {
						fail <- err
						return
					}
				}
				if err := c.syncJobs(ctx, ready.SessionID); err != nil {
					fail <- err
					return
				}
			}
		}
	}()
	go func() {
		defer workers.Done()
		defer telemetry.Recover(c.OnPanic, func() { fail <- telemetry.ErrPanic })
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ping, stop := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Ping(ping)
				stop()
				if err != nil {
					fail <- err
					return
				}
			}
		}
	}()
	select {
	case err = <-fail:
	case <-ctx.Done():
		err = ctx.Err()
	}
	cancel()
	conn.CloseNow()
	workers.Wait()
	return err
}

func (c *Client) Run(ctx context.Context) {
	defer telemetry.Recover(c.OnPanic, nil)
	delay := time.Second
	failures := 0
	for ctx.Err() == nil {
		start := time.Now()
		if c.OnState != nil {
			c.OnState("connecting")
		}
		err := c.Session(ctx)
		if ctx.Err() != nil {
			return
		}
		if c.OnState != nil {
			state := "failed"
			if errors.Is(err, errUnauthorized) {
				state = "unauthorized"
			}
			if errors.Is(err, errHandshake) {
				state = "handshake_failed"
			}
			c.OnState(state)
		}
		failures++
		if failures >= 5 && c.report != nil {
			c.report("cloud_unavailable")
		}
		if time.Since(start) > time.Minute {
			delay = time.Second
			failures = 0
		}
		timer := time.NewTimer(delay/2 + time.Duration(rand.Int64N(int64(delay/2)+1)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay = min(delay*2, 30*time.Second)
	}
}

func (c *Client) Report(ctx context.Context, e telemetry.Event) error {
	return c.request(ctx, "POST", "/events", "", e, nil)
}

// Scope durable commands to the origin and immutable device identity, not a rotating token.
func configSource(base, id string) string { return base + "#" + id }
func credentialID(token string) string    { return fmt.Sprintf("%x", sha256.Sum256([]byte(token))) }

type otaRemote struct{ client *Client }

func (r otaRemote) Source() string       { return configSource(r.client.base, r.client.id) }
func (r otaRemote) CredentialID() string { return credentialID(r.client.token) }
func (r otaRemote) Command(ctx context.Context) (*ota.Command, error) {
	var response struct {
		Command *ota.Command `json:"command"`
	}
	err := r.client.request(ctx, "GET", "/ota/command", "", nil, &response)
	return response.Command, err
}
func (r otaRemote) State(ctx context.Context, id string) (string, error) {
	var response struct {
		State string `json:"state"`
	}
	err := r.client.request(ctx, "GET", "/ota/"+id, "", nil, &response)
	return response.State, err
}
func (r otaRemote) Report(ctx context.Context, id string, report ota.Report) error {
	return r.client.request(ctx, "POST", "/ota/"+id+"/report", "", report, nil)
}
func (r otaRemote) Activate(ctx context.Context, id string, sequence int64) (bool, error) {
	var response struct {
		Allowed bool `json:"allowed"`
	}
	err := r.client.request(ctx, "POST", "/ota/"+id+"/activate", "", map[string]int64{"sequence": sequence}, &response)
	return response.Allowed, err
}
