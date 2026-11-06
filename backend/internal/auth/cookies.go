package auth

import (
	"net/http"
	"time"
)

// Cookies builds the auth cookies with consistent security attributes.
//
// Over HTTPS the names use the __Host- prefix: the browser then only accepts
// the cookie if it is Secure, has Path=/ and no Domain, so a compromised
// subdomain cannot set or overwrite it. Plain-HTTP development (localhost)
// cannot use Secure cookies in every browser, so the prefix is dropped there.
type Cookies struct {
	Secure bool
}

func (c Cookies) name(base string) string {
	if c.Secure {
		return "__Host-" + base
	}
	return base
}

// SessionName is the session cookie name.
func (c Cookies) SessionName() string { return c.name("session") }

// FlowName is the login-flow cookie name.
func (c Cookies) FlowName() string { return c.name("auth_flow") }

func (c Cookies) build(name, value string, maxAge time.Duration) *http.Cookie {
	// Secure comes from config: always true over HTTPS (and required by the
	// __Host- prefix); false only for plain-HTTP development on localhost.
	return &http.Cookie{ //nolint:gosec // G124: Secure is set from config, HttpOnly and SameSite are always set
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   int(maxAge.Seconds()),
		Secure:   c.Secure,
		HttpOnly: true, // not readable from JavaScript (XSS cannot steal it)
		// Lax: sent on top-level navigations (needed for the redirect back
		// from the provider) but not on cross-site POST/fetch (CSRF).
		SameSite: http.SameSiteLaxMode,
	}
}

// Session returns the session cookie.
func (c Cookies) Session(token string, ttl time.Duration) *http.Cookie {
	return c.build(c.SessionName(), token, ttl)
}

// Flow returns the login-flow cookie.
func (c Cookies) Flow(value string) *http.Cookie {
	return c.build(c.FlowName(), value, FlowTTL)
}

// Clear returns a cookie that deletes the named cookie in the browser.
func (c Cookies) Clear(name string) *http.Cookie {
	ck := c.build(name, "", 0) //nolint:gosec // G124: same attributes as build(); this only deletes the cookie
	ck.MaxAge = -1             // Max-Age=0 in the header: delete now
	return ck
}
