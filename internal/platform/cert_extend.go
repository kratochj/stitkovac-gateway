package platform

import (
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"net"
	"path/filepath"
)

// ExtendCertificate retains the private key and validity period. Only the public
// certificate is replaced atomically; the installer must record its new fingerprint.
func ExtendCertificate(dir string, ips []net.IP) (string, error) {
	pair, err := tls.LoadX509KeyPair(filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"))
	if err != nil {
		return "", err
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return "", err
	}
	changed := false
	for _, ip := range ips {
		found := false
		for _, existing := range cert.IPAddresses {
			if ip.Equal(existing) {
				found = true
				break
			}
		}
		if !found {
			cert.IPAddresses = append(cert.IPAddresses, ip)
			changed = true
		}
	}
	der := pair.Certificate[0]
	if changed {
		signer, ok := pair.PrivateKey.(crypto.Signer)
		if !ok {
			return "", x509.ErrUnsupportedAlgorithm
		}
		der, err = x509.CreateCertificate(rand.Reader, cert, cert, signer.Public(), signer)
		if err != nil {
			return "", err
		}
		if err := AtomicWrite(filepath.Join(dir, "tls.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
			return "", err
		}
	}
	digest := sha256.Sum256(der)
	return hex.EncodeToString(digest[:]), nil
}
