// Package auth implements sign-in with OpenID Connect (Authorization Code
// Flow + PKCE) and server-side sessions (backend-for-frontend pattern):
// OAuth tokens never reach the browser, which only holds an opaque,
// HttpOnly session cookie.
package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Identity is what we learn about a user from the identity provider.
type Identity struct {
	Issuer  string
	Subject string // stable user ID at the provider; email may change
	Email   string
	Name    string
	Picture string
}

// IdentityProvider is the part of OIDC the HTTP layer needs. It is an
// interface so handlers can be tested without a real provider.
type IdentityProvider interface {
	// AuthCodeURL is where the browser is sent to sign in.
	AuthCodeURL(state, nonce, verifier string) string
	// Exchange trades the authorization code for tokens, verifies the ID
	// token and returns the user's identity.
	Exchange(ctx context.Context, code, verifier, nonce string) (Identity, error)
}

// OIDCConfig configures the OIDC client.
type OIDCConfig struct {
	IssuerURL    string // e.g. https://accounts.google.com
	ClientID     string
	ClientSecret string
	RedirectURL  string // <APP_BASE_URL>/auth/callback
}

// OIDCProvider is an IdentityProvider backed by a real OIDC issuer.
type OIDCProvider struct {
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
	issuer   string
}

// NewOIDCProvider discovers the issuer's endpoints and signing keys from
// <issuer>/.well-known/openid-configuration. This makes a network request.
func NewOIDCProvider(ctx context.Context, cfg OIDCConfig) (*OIDCProvider, error) {
	provider, err := oidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery for %s: %w", cfg.IssuerURL, err)
	}
	return &OIDCProvider{
		oauth: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		},
		// Checks signature (keys fetched and cached from the JWKS endpoint),
		// issuer, audience (= our client ID) and expiry.
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		issuer:   cfg.IssuerURL,
	}, nil
}

// AuthCodeURL implements IdentityProvider.
func (p *OIDCProvider) AuthCodeURL(state, nonce, verifier string) string {
	return p.oauth.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.S256ChallengeOption(verifier), // PKCE: send SHA-256 of the verifier
		oauth2.SetAuthURLParam("prompt", "select_account"),
	)
}

// Errors returned by Exchange that callers may want to tell apart.
var (
	ErrNonceMismatch    = errors.New("id token nonce mismatch")
	ErrEmailNotVerified = errors.New("email address not verified by the provider")
)

// Exchange implements IdentityProvider.
func (p *OIDCProvider) Exchange(ctx context.Context, code, verifier, nonce string) (Identity, error) {
	// Back-channel request from our server to the provider's token endpoint,
	// authenticated with the client secret and the PKCE verifier.
	token, err := p.oauth.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Identity{}, fmt.Errorf("exchange code: %w", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return Identity{}, errors.New("token response has no id_token")
	}

	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return Identity{}, fmt.Errorf("verify id token: %w", err)
	}
	// The nonce binds the ID token to this browser's login attempt and
	// prevents replaying a token obtained elsewhere.
	if idToken.Nonce != nonce {
		return Identity{}, ErrNonceMismatch
	}

	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return Identity{}, fmt.Errorf("parse id token claims: %w", err)
	}
	if claims.Email == "" || !claims.EmailVerified {
		return Identity{}, ErrEmailNotVerified
	}
	if claims.Name == "" {
		claims.Name = claims.Email
	}

	return Identity{
		Issuer:  idToken.Issuer,
		Subject: idToken.Subject,
		Email:   claims.Email,
		Name:    claims.Name,
		Picture: claims.Picture,
	}, nil
}
