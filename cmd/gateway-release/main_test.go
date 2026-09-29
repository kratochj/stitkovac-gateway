package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestSigningKeyMustBePrivateAndEd25519(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "signing.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	parsed, err := privateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.Equal(key) {
		t.Fatal("wrong key")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := privateKey(path); err == nil {
		t.Fatal("accepted public-readable private key")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := path + "-link"
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := privateKey(link); err == nil {
		t.Fatal("followed key symlink")
	}
	if err := os.WriteFile(path, []byte("not a key"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := privateKey(path); err == nil {
		t.Fatal("accepted malformed private key")
	}
}
