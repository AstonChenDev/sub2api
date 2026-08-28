package repository

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const hfCiphertextPrefix = "hfenc:v1:"

type hfCredentialProtector struct {
	aead           cipher.AEAD
	fingerprintKey []byte
	initErr        error
}

// NewHFCredentialProtector deliberately uses a dedicated configuration key.
// It does not share the application's TOTP-generated AES key, so restarts can
// never make a large HF credential pool undecryptable.
func NewHFCredentialProtector(cfg *config.Config) service.HFCredentialProtector {
	p := &hfCredentialProtector{}
	if cfg == nil || strings.TrimSpace(cfg.HuggingFace.EncryptionKey) == "" {
		p.initErr = errors.New("huggingface encryption key is not configured")
		return p
	}
	key, err := hex.DecodeString(strings.TrimSpace(cfg.HuggingFace.EncryptionKey))
	if err != nil || len(key) != 32 {
		p.initErr = errors.New("huggingface encryption key must decode to 32 bytes")
		return p
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		p.initErr = fmt.Errorf("create huggingface cipher: %w", err)
		return p
	}
	p.aead, err = cipher.NewGCM(block)
	if err != nil {
		p.initErr = fmt.Errorf("create huggingface GCM: %w", err)
		return p
	}
	derive := hmac.New(sha256.New, key)
	_, _ = derive.Write([]byte("sub2api:huggingface:fingerprint:v1"))
	p.fingerprintKey = derive.Sum(nil)
	return p
}

func (p *hfCredentialProtector) Encrypt(plaintext string) (string, error) {
	if p == nil || p.initErr != nil || p.aead == nil {
		if p != nil && p.initErr != nil {
			return "", p.initErr
		}
		return "", errors.New("huggingface credential protector is unavailable")
	}
	nonce := make([]byte, p.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate credential nonce: %w", err)
	}
	sealed := p.aead.Seal(nil, nonce, []byte(plaintext), []byte(hfCiphertextPrefix))
	payload := append(nonce, sealed...)
	return hfCiphertextPrefix + base64.RawURLEncoding.EncodeToString(payload), nil
}

func (p *hfCredentialProtector) Decrypt(ciphertext string) (string, error) {
	if p == nil || p.initErr != nil || p.aead == nil {
		if p != nil && p.initErr != nil {
			return "", p.initErr
		}
		return "", errors.New("huggingface credential protector is unavailable")
	}
	if !strings.HasPrefix(ciphertext, hfCiphertextPrefix) {
		return "", errors.New("unsupported huggingface credential ciphertext")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(ciphertext, hfCiphertextPrefix))
	if err != nil || len(payload) < p.aead.NonceSize()+p.aead.Overhead() {
		return "", errors.New("invalid huggingface credential ciphertext")
	}
	nonce, sealed := payload[:p.aead.NonceSize()], payload[p.aead.NonceSize():]
	plaintext, err := p.aead.Open(nil, nonce, sealed, []byte(hfCiphertextPrefix))
	if err != nil {
		return "", errors.New("decrypt huggingface credential: authentication failed")
	}
	return string(plaintext), nil
}

func (p *hfCredentialProtector) Fingerprint(plaintext string) (string, error) {
	if p == nil || p.initErr != nil || len(p.fingerprintKey) == 0 {
		if p != nil && p.initErr != nil {
			return "", p.initErr
		}
		return "", errors.New("huggingface credential protector is unavailable")
	}
	mac := hmac.New(sha256.New, p.fingerprintKey)
	_, _ = mac.Write([]byte(plaintext))
	return hex.EncodeToString(mac.Sum(nil)), nil
}
