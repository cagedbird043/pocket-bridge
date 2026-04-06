package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"fmt"
)

const AlgorithmEd25519 = "ed25519"

func BuildChallenge(deviceID string) ([]byte, error) {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("read random nonce: %w", err)
	}
	prefix := []byte("pocket-bridge-auth-v1")
	challenge := make([]byte, 0, len(prefix)+1+len(deviceID)+1+len(nonce))
	challenge = append(challenge, prefix...)
	challenge = append(challenge, 0)
	challenge = append(challenge, []byte(deviceID)...)
	challenge = append(challenge, 0)
	challenge = append(challenge, nonce...)
	return challenge, nil
}

func ParsePrivateKeyBase64(value string) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode private key base64: %w", err)
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("parse PKCS8 private key: %w", err)
	}
	key, ok := keyAny.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is not Ed25519")
	}
	return key, nil
}

func ParsePublicKeyBase64(value string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode public key base64: %w", err)
	}
	keyAny, err := x509.ParsePKIXPublicKey(raw)
	if err != nil {
		return nil, fmt.Errorf("parse PKIX public key: %w", err)
	}
	key, ok := keyAny.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key is not Ed25519")
	}
	return key, nil
}

func SignChallenge(privateKey ed25519.PrivateKey, challenge []byte) []byte {
	return ed25519.Sign(privateKey, challenge)
}

func VerifyChallenge(publicKey ed25519.PublicKey, challenge []byte, signature []byte) bool {
	return ed25519.Verify(publicKey, challenge, signature)
}

func GenerateKeyPairBase64() (publicKeyBase64 string, privateKeyBase64 string, err error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate Ed25519 keypair: %w", err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return "", "", fmt.Errorf("marshal private key: %w", err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return "", "", fmt.Errorf("marshal public key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(publicDER), base64.StdEncoding.EncodeToString(privateDER), nil
}
