// Server-made SSH keys: each remote gets its own ed25519 key. The private
// half is sealed under the server key like any credential and never leaves
// the sealed form except in memory for one push; only the public half is
// shown, for the owner to install on the remote.
package git

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"strings"

	"golang.org/x/crypto/ssh"
)

// ErrForeignKey is returned for a stored key this server cannot open.
var ErrForeignKey = errors.New("that key was not made by this server")

// keyComment labels generated keys on the remote's side.
const keyComment = "yana"

// NewKey makes an ed25519 key pair. publicKey is the authorized_keys line;
// sealedKey is the private key, sealed and base64-encoded for storage.
func (l *Layer) NewKey() (publicKey, sealedKey string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, keyComment)
	if err != nil {
		return "", "", err
	}
	sealed, err := l.Seal(string(pem.EncodeToMemory(block)))
	if err != nil {
		return "", "", err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", err
	}
	return authorizedLine(sshPub), base64.StdEncoding.EncodeToString(sealed), nil
}

// OpenKey checks that sealedKey was made and sealed by this server. It
// returns the raw sealed bytes for the database and the public key
// derived from the private one.
func (l *Layer) OpenKey(sealedKey string) (sealed []byte, publicKey string, err error) {
	sealed, err = base64.StdEncoding.DecodeString(sealedKey)
	if err != nil {
		return nil, "", ErrForeignKey
	}
	pemText, err := l.open(sealed)
	if err != nil {
		return nil, "", ErrForeignKey
	}
	signer, err := ssh.ParsePrivateKey([]byte(pemText))
	if err != nil {
		return nil, "", ErrForeignKey
	}
	return sealed, authorizedLine(signer.PublicKey()), nil
}

func authorizedLine(k ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k))) + " " + keyComment
}
