package sshprofiles

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/codex-switch/admin-go/internal/platform"
)

const credentialKeySetting = "SSH_CREDENTIALS_ENCRYPTION_KEY"
const credentialEnvelopeVersion = "v1:"
const maxPasswordBytes = 4096
const maxPrivateKeyBytes = 65536

type credentials struct {
	Password   *string `json:"password,omitempty"`
	PrivateKey *string `json:"privateKey,omitempty"`
	Passphrase *string `json:"passphrase,omitempty"`
}

func validSecret(value *string, limit int) bool {
	return value == nil || (utf8.ValidString(*value) && len(*value) <= limit && !strings.ContainsRune(*value, 0))
}

func (value credentials) valid(auth string) bool {
	if !validSecret(value.Password, maxPasswordBytes) || !validSecret(value.PrivateKey, maxPrivateKeyBytes) ||
		!validSecret(value.Passphrase, maxPasswordBytes) {
		return false
	}
	if auth == "password" {
		return value.PrivateKey == nil && value.Passphrase == nil
	}
	return value.Password == nil && (value.Passphrase == nil || value.PrivateKey != nil)
}

func (value credentials) empty() bool {
	return value.Password == nil && value.PrivateKey == nil && value.Passphrase == nil
}

func unavailableCredentials() error {
	return platform.NewError(503, "client.ssh.credentialsUnavailable")
}

func credentialCipher(config platform.Config) (cipher.AEAD, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(config.Get(credentialKeySetting, "")))
	if err != nil || len(key) != 32 {
		return nil, unavailableCredentials()
	}
	block, err := aes.NewCipher(key)
	clear(key)
	if err != nil {
		return nil, unavailableCredentials()
	}
	return cipher.NewGCM(block)
}

// Bind ciphertext to its owner and login target; changing a display name does not invalidate it.
func credentialContext(owner string, value profile) []byte {
	context, _ := json.Marshal([]interface{}{"remoteai:ssh-credentials:v1", owner, value.ID,
		value.Host, value.Port, value.Username, value.Auth}) // Only primitive values; Marshal cannot fail.
	return context
}

func sealCredentials(config platform.Config, owner string, value profile, secret credentials) (string, error) {
	if secret.empty() {
		return "", nil
	}
	aead, err := credentialCipher(config)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", unavailableCredentials()
	}
	plain, err := json.Marshal(secret)
	if err != nil {
		return "", unavailableCredentials()
	}
	defer clear(plain)
	sealed := aead.Seal(nonce, nonce, plain, credentialContext(owner, value))
	return credentialEnvelopeVersion + base64.StdEncoding.EncodeToString(sealed), nil
}

func openCredentials(config platform.Config, row record) (*credentials, error) {
	if row.EncryptedCredentials == nil {
		return nil, nil
	}
	aead, err := credentialCipher(config)
	if err != nil {
		return nil, err
	}
	encoded, versioned := strings.CutPrefix(*row.EncryptedCredentials, credentialEnvelopeVersion)
	sealed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || !versioned || len(sealed) < aead.NonceSize()+aead.Overhead() {
		return nil, unavailableCredentials()
	}
	plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():],
		credentialContext(row.OwnerID, *row.Config))
	if err != nil {
		return nil, unavailableCredentials()
	}
	defer clear(plain)
	var result credentials
	if json.Unmarshal(plain, &result) != nil || !result.valid(row.Config.Auth) {
		return nil, unavailableCredentials()
	}
	return &result, nil
}
