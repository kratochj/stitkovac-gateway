// Package update stores signed application releases independently of print data.
package update

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
)

const (
	MaxManifest      = 16 << 10
	MaxArtifact      = 128 << 20
	LauncherProtocol = 2
)

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var keyPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

type Manifest struct {
	Schema           int    `json:"schema"`
	Version          string `json:"version"`
	Platform         string `json:"platform"`
	Size             int64  `json:"size"`
	SHA256           string `json:"sha256"`
	LauncherProtocol int    `json:"launcherProtocol"`
}

// Payload contains the exact signed JSON bytes, encoded as base64 by encoding/json.
// The signature domain prevents reuse of signatures for other protocols.
type Envelope struct {
	KeyID     string `json:"keyId"`
	Payload   []byte `json:"payload"`
	Signature []byte `json:"signature"`
}

const signatureDomain = "stitkovac-gateway-release-v1\x00"

func strictJSON(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

func Verify(b []byte, keys map[string]ed25519.PublicKey, platform string) (Manifest, error) {
	var e Envelope
	var m Manifest
	if len(b) > MaxManifest || strictJSON(b, &e) != nil {
		return m, errors.New("invalid release envelope")
	}
	key := keys[e.KeyID]
	if !keyPattern.MatchString(e.KeyID) || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, append([]byte(signatureDomain), e.Payload...), e.Signature) {
		return m, errors.New("untrusted release signature")
	}
	if strictJSON(e.Payload, &m) != nil || m.Schema != 1 || !versionPattern.MatchString(m.Version) || m.Platform != platform || m.Size < 1 || m.Size > MaxArtifact || !digestPattern.MatchString(m.SHA256) || (m.LauncherProtocol < 1 || m.LauncherProtocol > LauncherProtocol) {
		return Manifest{}, errors.New("incompatible release manifest")
	}
	return m, nil
}

func newer(a, b string) bool {
	av, bv := strings.Split(a, "."), strings.Split(b, ".")
	for i := range av {
		x, _ := strconv.Atoi(av[i])
		y, _ := strconv.Atoi(bv[i])
		if x != y {
			return x > y
		}
	}
	return false
}

// ValidVersion is the stable release grammar shared with the control protocol.
func ValidVersion(v string) bool { return versionPattern.MatchString(v) }
