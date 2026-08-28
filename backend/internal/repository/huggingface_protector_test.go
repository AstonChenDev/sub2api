package repository

import (
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestHFCredentialProtectorRoundTripAndAuthentication(t *testing.T) {
	cfg := &config.Config{HuggingFace: config.HuggingFaceConfig{EncryptionKey: strings.Repeat("ab", 32)}}
	protector := NewHFCredentialProtector(cfg)

	ciphertext1, err := protector.Encrypt("hf_secret_token")
	require.NoError(t, err)
	ciphertext2, err := protector.Encrypt("hf_secret_token")
	require.NoError(t, err)
	require.NotEqual(t, ciphertext1, ciphertext2, "GCM nonces must randomize equal plaintexts")

	plaintext, err := protector.Decrypt(ciphertext1)
	require.NoError(t, err)
	require.Equal(t, "hf_secret_token", plaintext)
	fingerprint1, err := protector.Fingerprint("hf_secret_token")
	require.NoError(t, err)
	fingerprint2, err := protector.Fingerprint("hf_secret_token")
	require.NoError(t, err)
	require.Equal(t, fingerprint1, fingerprint2)
	require.NotContains(t, fingerprint1, "secret")

	tamperAt := len(hfCiphertextPrefix) + 5
	replacement := byte('A')
	if ciphertext1[tamperAt] == replacement {
		replacement = 'B'
	}
	tamperedBytes := []byte(ciphertext1)
	tamperedBytes[tamperAt] = replacement
	tampered := string(tamperedBytes)
	_, err = protector.Decrypt(tampered)
	require.Error(t, err)

	other := NewHFCredentialProtector(&config.Config{HuggingFace: config.HuggingFaceConfig{EncryptionKey: strings.Repeat("cd", 32)}})
	_, err = other.Decrypt(ciphertext1)
	require.Error(t, err)
}
