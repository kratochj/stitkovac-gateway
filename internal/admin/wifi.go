package admin

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"time"

	"github.com/kratochj/stitkovac-gateway/internal/telemetry"
)

type wifiHostKey struct{}

// Every request rechecks the root-owned setting, including existing keep-alive
// connections. A missing helper fails closed without affecting Ethernet access.
func (s *Server) wifiHandler(address string) http.Handler {
	next := s.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		status, err := s.Network.Status(ctx)
		local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
		if err != nil || status.WiFiAdminAddress() != address || r.Host != address || !ok || local.String() != address {
			http.Error(w, "Přístup přes Wi-Fi není dostupný.", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), wifiHostKey{}, address)))
	})
}

// ServeWiFi follows DHCP without binding a wildcard socket or restarting print
// workers. Only the extra HTTPS server is closed when access is disabled.
func (s *Server) ServeWiFi(ctx context.Context, config *tls.Config) {
	defer telemetry.Recover(s.OnPanic, nil)
	s.serveWiFi(ctx, config, 2*time.Second, func(ctx context.Context, address string) (net.Listener, error) {
		var lc net.ListenConfig
		return lc.Listen(ctx, "tcp4", address)
	})
}

func (s *Server) serveWiFi(ctx context.Context, config *tls.Config, interval time.Duration, listen func(context.Context, string) (net.Listener, error)) {
	if s.Network == nil {
		return
	}
	var active *http.Server
	var address string
	var ended chan struct{}
	closeActive := func() {
		if active != nil {
			_ = active.Close()
			<-ended
			active = nil
		}
		address = ""
	}
	defer closeActive()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		probe, cancel := context.WithTimeout(ctx, time.Second)
		status, err := s.Network.Status(probe)
		cancel()
		wanted := ""
		if err == nil {
			wanted = status.WiFiAdminAddress()
		}
		if active != nil {
			select {
			case <-ended:
				closeActive()
			default:
			}
		}
		if wanted != address {
			closeActive()
			if wanted != "" && ctx.Err() == nil {
				listener, err := listen(ctx, wanted)
				if err == nil {
					active = &http.Server{Handler: s.wifiHandler(wanted), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
					address = wanted
					ended = make(chan struct{})
					go func(server *http.Server, done chan struct{}) {
						defer close(done)
						defer telemetry.Recover(s.OnPanic, nil)
						_ = server.Serve(tls.NewListener(listener, config))
					}(active, ended)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
