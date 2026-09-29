// gateway-release runs on the trusted release workstation, never on a gateway.
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/kratochj/stitkovac-gateway/internal/update"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func privateKey(path string) (ed25519.PrivateKey, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 16<<10 {
		return nil, errors.New("signing key must be a private regular PKCS8 PEM file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, rest := pem.Decode(b)
	if block == nil || block.Type != "PRIVATE KEY" || len(rest) != 0 {
		return nil, errors.New("invalid signing key encoding")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("invalid signing key")
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("signing key must be Ed25519")
	}
	return key, nil
}
func run(args []string) error {
	flags := flag.NewFlagSet("gateway-release", flag.ContinueOnError)
	artifact := flags.String("artifact", "", "Linux ARM64 gateway executable")
	version := flags.String("version", "", "Stable release version matching the build")
	keyFile := flags.String("key-file", "", "Private PKCS8 Ed25519 PEM file on the release workstation")
	keyID := flags.String("key-id", "", "Provisioned signing key identifier")
	output := flags.String("manifest-out", "", "New signed manifest file; existing files are never replaced")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	key, err := privateKey(*keyFile)
	if err != nil {
		return err
	}
	binary, err := elf.Open(*artifact)
	if err != nil {
		return errors.New("artifact is not an ELF executable")
	}
	supported := binary.Class == elf.ELFCLASS64 && binary.Machine == elf.EM_AARCH64 && (binary.Type == elf.ET_EXEC || binary.Type == elf.ET_DYN)
	binary.Close()
	if !supported {
		return errors.New("artifact must target Linux ARM64")
	}
	f, err := os.Open(*artifact)
	if err != nil {
		return err
	}
	defer f.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(f, update.MaxArtifact+1))
	if err != nil {
		return err
	}
	manifest := update.Manifest{Schema: 1, Version: *version, Platform: "linux-arm64", Size: size, SHA256: hex.EncodeToString(hash.Sum(nil)), LauncherProtocol: update.LauncherProtocol}
	payload, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	// Keep the signature domain stable across producer and independent verifier.
	envelope := update.Envelope{KeyID: *keyID, Payload: payload, Signature: ed25519.Sign(key, append([]byte("stitkovac-gateway-release-v1\x00"), payload...))}
	b, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	public := map[string]ed25519.PublicKey{*keyID: key.Public().(ed25519.PublicKey)}
	if _, err := update.Verify(b, public, "linux-arm64"); err != nil {
		return err
	}
	out, err := os.OpenFile(*output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	if _, err = out.Write(b); err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	// Only public trust anchors are emitted; the private key is never copied.
	return json.NewEncoder(os.Stdout).Encode(public)
}
