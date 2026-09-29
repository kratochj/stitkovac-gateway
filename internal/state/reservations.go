package state

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/netip"
	"time"
)

type Pool struct {
	Network             netip.Prefix
	Server, First, Last netip.Addr
}

func (p Pool) Validate() error {
	if !p.Network.IsValid() || !p.Network.Addr().Is4() || p.Network.Bits() < 16 || p.Network.Bits() > 29 || p.Network != p.Network.Masked() {
		return errors.New("use an aligned IPv4 subnet between /16 and /29")
	}
	for _, a := range []netip.Addr{p.Server, p.First, p.Last} {
		if !a.Is4() || !a.IsPrivate() || !p.Network.Contains(a) || a == p.Network.Addr() || !p.Network.Contains(a.Next()) {
			return errors.New("invalid private subnet address")
		}
	}
	if p.First.Compare(p.Last) > 0 || (p.Server.Compare(p.First) >= 0 && p.Server.Compare(p.Last) <= 0) {
		return errors.New("invalid address pool")
	}
	return nil
}

func MAC(raw string) (string, error) {
	m, err := net.ParseMAC(raw)
	if err != nil || len(m) != 6 || m[0]&1 != 0 || m.String() == "00:00:00:00:00:00" {
		return "", errors.New("invalid unicast Ethernet MAC")
	}
	return m.String(), nil
}

type Reservation struct {
	MAC        string `json:"mac"`
	IP         string `json:"ip"`
	CreatedAt  int64  `json:"createdAt"`
	LastSeen   int64  `json:"lastSeen"`
	LeaseUntil int64  `json:"leaseUntil"`
	Declined   bool   `json:"declined"`
}

// Reserve commits ownership before the caller sends either OFFER or ACK.
// Reservations never expire and DHCP client identifiers are deliberately unused.
func (s *Store) Reserve(ctx context.Context, raw string, pool Pool, now time.Time) (Reservation, error) {
	if err := pool.Validate(); err != nil {
		return Reservation{}, err
	}
	mac, err := MAC(raw)
	if err != nil {
		return Reservation{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Reservation{}, err
	}
	defer tx.Rollback()
	r := Reservation{MAC: mac}
	err = tx.QueryRowContext(ctx, "SELECT ip, created_at, last_seen, lease_until, declined FROM reservations WHERE mac=?", mac).Scan(&r.IP, &r.CreatedAt, &r.LastSeen, &r.LeaseUntil, &r.Declined)
	if err == nil {
		ip, parseErr := netip.ParseAddr(r.IP)
		if parseErr != nil || !pool.Network.Contains(ip) || ip == pool.Server || ip.Compare(pool.First) < 0 || ip.Compare(pool.Last) > 0 || r.Declined {
			return Reservation{}, ErrConflict
		}
		return r, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Reservation{}, err
	}
	for ip := pool.First; ip.Compare(pool.Last) <= 0; ip = ip.Next() {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM reservations WHERE ip=?", ip.String()).Scan(&n); err != nil {
			return Reservation{}, err
		}
		if n != 0 {
			continue
		}
		r.IP, r.CreatedAt, r.LastSeen = ip.String(), now.Unix(), now.Unix()
		if _, err := tx.ExecContext(ctx, "INSERT INTO reservations(mac,ip,created_at,last_seen) VALUES(?,?,?,?)", mac, r.IP, r.CreatedAt, r.LastSeen); err != nil {
			return Reservation{}, err
		}
		return r, tx.Commit()
	}
	return Reservation{}, errors.New("DHCP address pool exhausted")
}

// Lease records the lease before an ACK can leave the gateway.
func (s *Store) Lease(ctx context.Context, mac, ip string, now time.Time, duration time.Duration) error {
	res, err := s.db.ExecContext(ctx, "UPDATE reservations SET last_seen=?, lease_until=? WHERE mac=? AND ip=? AND declined=0", now.Unix(), now.Add(duration).Unix(), mac, ip)
	return changed(res, err)
}

func (s *Store) Release(ctx context.Context, mac, ip string, declined bool) error {
	res, err := s.db.ExecContext(ctx, "UPDATE reservations SET lease_until=0, declined=MAX(declined, ?) WHERE mac=? AND ip=?", declined, mac, ip)
	return changed(res, err)
}

func (s *Store) Reservations(ctx context.Context) ([]Reservation, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT mac,ip,created_at,last_seen,lease_until,declined FROM reservations ORDER BY created_at,ip")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Reservation{}
	for rows.Next() {
		var r Reservation
		if err := rows.Scan(&r.MAC, &r.IP, &r.CreatedAt, &r.LastSeen, &r.LeaseUntil, &r.Declined); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func changed(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
