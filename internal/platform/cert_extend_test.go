package platform

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestExtendCertificatePreservesKeyAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	original, err := Certificate(dir, []net.IP{net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	key, _ := os.ReadFile(filepath.Join(dir, "tls.key"))
	fingerprint, err := ExtendCertificate(dir, []net.IP{net.IPv4(192, 168, 78, 1)})
	if err != nil || fingerprint == original {
		t.Fatal(err)
	}
	current, _ := os.ReadFile(filepath.Join(dir, "tls.key"))
	if string(key) != string(current) {
		t.Fatal("private key changed")
	}
	pair, err := tls.LoadX509KeyPair(filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"))
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || cert.VerifyHostname("192.168.78.1") != nil || cert.VerifyHostname("127.0.0.1") != nil {
		t.Fatal("missing SAN")
	}
	again, err := ExtendCertificate(dir, []net.IP{net.IPv4(192, 168, 78, 1)})
	if err != nil || again != fingerprint {
		t.Fatal("not idempotent")
	}
}
