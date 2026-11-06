package httpapi

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// contentSecurityPolicy tells the browser where the page may load code and
// data from. It mainly limits the damage of an XSS bug: injected markup
// cannot load scripts from other sites, send data elsewhere or be framed.
//
//   - Scripts, styles, fonts, fetch: only our own origin. No inline scripts.
//   - Images: ours, data: URIs, and Google profile pictures (avatars).
//   - No plugins (<object>), no <base> tricks, no framing (clickjacking).
var contentSecurityPolicy = strings.Join([]string{
	"default-src 'self'",
	"script-src 'self'",
	"style-src 'self'",
	"img-src 'self' data: https://*.googleusercontent.com",
	"font-src 'self'",
	"connect-src 'self'",
	"object-src 'none'",
	"base-uri 'self'",
	"form-action 'self'",
	"frame-ancestors 'none'",
}, "; ")

// securityHeaders sets browser security headers on every response.
// hsts is enabled only when the site is served over HTTPS: sending it over
// plain HTTP is ignored by browsers and would only confuse local setups.
func securityHeaders(hsts bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", contentSecurityPolicy)
			h.Set("X-Content-Type-Options", "nosniff") // no MIME sniffing
			h.Set("X-Frame-Options", "DENY")           // clickjacking, older browsers
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Cross-Origin-Opener-Policy", "same-origin") // isolates our window from popups
			h.Set("Cross-Origin-Resource-Policy", "same-origin")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
			if hsts {
				// Two years; browsers will refuse plain HTTP for this host.
				h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// MaxBodyBytes caps request bodies. The largest legitimate request is an
// event with a 5000-character description (≈ 20 KB of UTF-8).
const MaxBodyBytes = 64 << 10

// limitBody makes reading more than MaxBodyBytes from the body fail, so a
// client cannot exhaust memory with a huge upload. Handlers see the error
// from the JSON decoder and answer 413 (see decodeJSON in step 9).
func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > MaxBodyBytes {
			writeError(w, r, http.StatusRequestEntityTooLarge, "body_too_large",
				fmt.Sprintf("request body must not exceed %d bytes", MaxBodyBytes))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
		next.ServeHTTP(w, r)
	})
}

// csrfProtection rejects cross-site state-changing requests (POST, PUT,
// PATCH, DELETE) using the browser's Sec-Fetch-Site header, or Origin vs
// Host when it is absent. This is the second line of defense after
// SameSite=Lax cookies, and it also covers same-site attacks (e.g. from a
// sibling subdomain) that SameSite does not stop.
func csrfProtection(baseURL *url.URL) (Middleware, error) {
	cop := http.NewCrossOriginProtection()
	if baseURL != nil {
		// Our own public origin is always allowed, even if a proxy rewrites
		// the Host header.
		if err := cop.AddTrustedOrigin(baseURL.Scheme + "://" + baseURL.Host); err != nil {
			return nil, fmt.Errorf("csrf trusted origin: %w", err)
		}
	}
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logFrom(r.Context()).WarnContext(r.Context(), "cross-origin request blocked",
			"origin", r.Header.Get("Origin"), "sec_fetch_site", r.Header.Get("Sec-Fetch-Site"))
		writeError(w, r, http.StatusForbidden, "cross_origin", "cross-origin request blocked")
	}))
	return cop.Handler, nil
}
