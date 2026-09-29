package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"

	"github.com/kratochj/stitkovac-gateway/internal/platform"
	"github.com/kratochj/stitkovac-gateway/internal/telemetry"
)

type Status struct {
	URL      string
	TokenSet bool
	State    string
}

type Connection interface {
	Run(context.Context)
	Report(context.Context, telemetry.Event) error
}

type Factory func(Config, func(string)) (Connection, error)

// Manager serializes configuration changes and never overlaps two cloud workers.
// A cancelled worker completes any already-authorized local TCP write before exiting.
type Manager struct {
	mu          sync.RWMutex
	config      Config
	status      Status
	client      Connection
	path        string
	id          string
	changed     chan struct{}
	factory     Factory
	paused      bool
	pauseDone   chan struct{}
	pauseClosed bool
}

func NewManager(dir, id string, cfg Config, factory Factory) *Manager {
	status := Status{URL: cfg.URL, TokenSet: cfg.Token != "", State: "unconfigured"}
	if cfg.Token != "" {
		status.State = "connecting"
	}
	return &Manager{id: id, config: cfg, status: status, path: filepath.Join(dir, "cloud.json"), changed: make(chan struct{}, 1), factory: factory}
}

func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

func (m *Manager) Save(base, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.paused {
		return errors.New("gateway update is activating")
	}
	// A blank field may retain a token only for the exact same normalized origin.
	// Never send an existing credential to a newly entered server.
	if strings.TrimSpace(token) == "" {
		candidate, err := NormalizeConfig(base, m.config.Token)
		if err != nil || candidate.URL != m.config.URL {
			return ErrNewTokenRequired
		}
		token = m.config.Token
	}
	cfg, err := NormalizeConfig(base, token)
	if err != nil {
		return err
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return errors.New("configuration encoding failed")
	}
	if err := platform.AtomicWrite(m.path, b, 0600); err != nil {
		return ErrConfigStorage
	}
	m.config = cfg
	m.status = Status{URL: cfg.URL, TokenSet: true, State: "applying"}
	select {
	case m.changed <- struct{}{}:
	default:
	}
	return nil
}

func (m *Manager) Run(ctx context.Context) {
	var cancel context.CancelFunc
	var stopped chan struct{}
	for {
		// Withdraw telemetry before stopping the old connection. Report holds the read
		// lock for its bounded request, so credentials cannot switch during delivery.
		m.mu.Lock()
		m.client = nil
		m.mu.Unlock()
		if cancel != nil {
			cancel()
			<-stopped
		}
		if ctx.Err() != nil {
			return
		}
		m.mu.Lock()
		cfg := m.config
		if m.paused {
			cfg = Config{}
			m.status.State = "updating"
			if !m.pauseClosed {
				close(m.pauseDone)
				m.pauseClosed = true
			}
		}
		// The latest persisted configuration supersedes all queued notifications.
		select {
		case <-m.changed:
		default:
		}
		if cfg.Token != "" {
			m.status.State = "connecting"
		}
		m.mu.Unlock()
		cancel, stopped = nil, nil
		if cfg.Token != "" {
			client, err := m.factory(cfg, func(state string) {
				m.mu.Lock()
				defer m.mu.Unlock()
				if m.config == cfg && m.status.State != "applying" && !m.paused {
					m.status.State = state
				}
			})
			if err != nil {
				m.mu.Lock()
				m.status.State = "failed"
				m.mu.Unlock()
			} else {
				child, stop := context.WithCancel(ctx)
				cancel, stopped = stop, make(chan struct{})
				m.mu.Lock()
				m.client = client
				m.mu.Unlock()
				done := stopped
				go func() { defer close(done); client.Run(child) }()
			}
		}
		select {
		case <-ctx.Done():
		case <-m.changed:
		}
	}
}

func (m *Manager) Report(ctx context.Context, e telemetry.Event) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.client == nil {
		return errors.New("cloud is not connected")
	}
	return m.client.Report(ctx, e)
}

// Pause stops claiming work and waits for every already-authorized local write.
func (m *Manager) Pause(ctx context.Context) error {
	m.mu.Lock()
	if !m.paused {
		m.paused = true
		m.pauseDone = make(chan struct{})
		m.pauseClosed = false
		m.status.State = "updating"
	}
	done := m.pauseDone
	m.mu.Unlock()
	select {
	case m.changed <- struct{}{}:
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *Manager) Resume() {
	m.mu.Lock()
	m.paused = false
	m.mu.Unlock()
	select {
	case m.changed <- struct{}{}:
	default:
	}
}

func (m *Manager) Source() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return configSource(m.config.URL, m.id)
}
