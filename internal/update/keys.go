package update

import "crypto/ed25519"

// ReadKeys reads only public trust anchors. Provision this file on the read-only
// system; never derive trusted keys from a downloaded release manifest.
func ReadKeys(path string) (map[string]ed25519.PublicKey, error) {
	b, err := readFile(path, MaxManifest)
	if err != nil {
		return nil, err
	}
	keys := map[string]ed25519.PublicKey{}
	if err := strictJSON(b, &keys); err != nil {
		return nil, err
	}
	return keys, nil
}
