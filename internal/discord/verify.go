package discord

import (
	"crypto/ed25519"
	"encoding/hex"
)

// Verify checks a Discord interaction request signature.
//
// Discord signs every interaction webhook call with the application's Ed25519
// key over (timestamp || rawBody). The /interactions endpoint must be publicly
// reachable so Discord can call it, so this signature check is the only thing
// standing between the public internet and the handler: any request that fails
// it is rejected before parsing.
func Verify(publicKeyHex, signatureHex, timestamp string, body []byte) bool {
	pub, err := hex.DecodeString(publicKeyHex)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	sig, err := hex.DecodeString(signatureHex)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	msg := make([]byte, 0, len(timestamp)+len(body))
	msg = append(msg, timestamp...)
	msg = append(msg, body...)
	return ed25519.Verify(ed25519.PublicKey(pub), msg, sig)
}
