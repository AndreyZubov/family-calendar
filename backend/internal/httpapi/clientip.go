package httpapi

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type clientIPKey struct{}

// clientIP determines the real client address and stores it in the context.
//
// Behind Caddy every request comes from Caddy's address, so the client is
// taken from X-Forwarded-For — but only when the direct peer is a trusted
// proxy. Otherwise anyone could send a fake X-Forwarded-For and dodge the
// rate limiter or forge log entries.
//
// The header is read right to left: each proxy appends the address it saw,
// so the rightmost untrusted entry is the first one a trusted proxy vouches
// for. Entries further left were written by the client and are ignored.
func clientIP(trusted []netip.Prefix) Middleware {
	isTrusted := func(a netip.Addr) bool {
		for _, p := range trusted {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := peerAddr(r.RemoteAddr)
			if ip.IsValid() && isTrusted(ip) {
				hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
				for i := len(hops) - 1; i >= 0; i-- {
					a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
					if err != nil {
						break // malformed: stop at the last address we could trust
					}
					ip = a.Unmap()
					if !isTrusted(ip) {
						break
					}
				}
			}
			ctx := context.WithValue(r.Context(), clientIPKey{}, ip)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ClientIPFrom returns the client address set by the clientIP middleware.
func ClientIPFrom(ctx context.Context) netip.Addr {
	ip, _ := ctx.Value(clientIPKey{}).(netip.Addr)
	return ip
}

func peerAddr(remoteAddr string) netip.Addr {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap() // ::ffff:1.2.3.4 -> 1.2.3.4
}
