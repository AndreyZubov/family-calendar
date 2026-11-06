package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// FlowState is what the server must remember between redirecting the
// browser to the provider (/auth/login) and the provider redirecting it back
// (/auth/callback). It is kept in a short-lived encrypted cookie, so no
// database row is needed for half-finished logins.
type FlowState struct {
	State    string    `json:"s"` // CSRF protection for the callback
	Nonce    string    `json:"n"` // binds the ID token to this login
	Verifier string    `json:"v"` // PKCE code verifier (secret)
	ReturnTo string    `json:"r"` // local path to open after login
	Expires  time.Time `json:"e"`
}

// FlowTTL is how long a user has to complete the login at the provider.
const FlowTTL = 10 * time.Minute

// FlowSealer encrypts and authenticates FlowState with AES-256-GCM.
// Encryption hides the PKCE verifier; authentication makes the cookie
// tamper-proof.
type FlowSealer struct {
	aead cipher.AEAD
	now  func() time.Time
}

// NewFlowSealer derives a dedicated key from the application secret, so the
// same secret can safely be reused for other purposes later.
func NewFlowSealer(secret []byte) (*FlowSealer, error) {
	if len(secret) < 32 {
		return nil, errors.New("secret must be at least 32 bytes")
	}
	key, err := hkdf.Key(sha256.New, secret, nil, "family-calendar auth flow cookie v1", 32)
	if err != nil {
		return nil, fmt.Errorf("derive key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &FlowSealer{aead: aead, now: time.Now}, nil
}

// Seal returns the cookie value for st.
func (s *FlowSealer) Seal(st FlowState) (string, error) {
	plain, err := json.Marshal(st)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, s.aead.NonceSize())
	_, _ = rand.Read(nonce)
	sealed := s.aead.Seal(nonce, nonce, plain, nil) // nonce || ciphertext || tag
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// ErrInvalidFlow means the cookie is missing, forged, corrupted or expired.
var ErrInvalidFlow = errors.New("invalid or expired login flow")

// Open decrypts and validates a cookie value produced by Seal.
func (s *FlowSealer) Open(value string) (FlowState, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) < s.aead.NonceSize() {
		return FlowState{}, ErrInvalidFlow
	}
	nonce, ciphertext := raw[:s.aead.NonceSize()], raw[s.aead.NonceSize():]
	plain, err := s.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return FlowState{}, ErrInvalidFlow // wrong key or modified cookie
	}
	var st FlowState
	if err := json.Unmarshal(plain, &st); err != nil {
		return FlowState{}, ErrInvalidFlow
	}
	if !s.now().Before(st.Expires) {
		return FlowState{}, ErrInvalidFlow
	}
	return st, nil
}

// NewFlowState creates fresh random state, nonce and PKCE verifier.
func NewFlowState(returnTo string, now time.Time) FlowState {
	return FlowState{
		State:    randomToken(),
		Nonce:    randomToken(),
		Verifier: randomToken(), // 43 chars of base64url: valid PKCE verifier
		ReturnTo: SafeReturnTo(returnTo),
		Expires:  now.Add(FlowTTL),
	}
}

// SafeReturnTo allows only local absolute paths, so the login redirect
// cannot be abused to send users to another site (open redirect).
func SafeReturnTo(p string) string {
	if p == "" || p[0] != '/' ||
		strings.HasPrefix(p, "//") || strings.HasPrefix(p, `/\`) ||
		strings.ContainsAny(p, "\r\n\t") {
		return "/"
	}
	return p
}

// randomToken returns 32 random bytes as base64url (43 characters).
func randomToken() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}
