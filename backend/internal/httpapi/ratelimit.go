package httpapi

import (
	"context"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimit is a token bucket: Burst requests at once, refilled at Every.
type RateLimit struct {
	Every time.Duration // one new token per this interval
	Burst int
}

// Default limits per client IP. Generous for a family (a page load makes a
// handful of API calls), tight enough to slow down scripted abuse.
var (
	// Sign-in: a full login is 2 requests (login + callback).
	AuthRateLimit = RateLimit{Every: 3 * time.Second, Burst: 10} // ~20/min sustained
	// API: covers bursts from the calendar view.
	APIRateLimit = RateLimit{Every: 50 * time.Millisecond, Burst: 60} // ~20/s sustained
)

const (
	maxTrackedIPs = 10_000           // memory cap: ~100 bytes per entry
	idleAfter     = 10 * time.Minute // forget clients that went quiet
)

// ipLimiter keeps one token bucket per client IP, in memory. One app
// instance is enough here; several instances would each count separately.
type ipLimiter struct {
	limit RateLimit
	now   func() time.Time

	mu      sync.Mutex
	clients map[netip.Addr]*client
}

type client struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

func newIPLimiter(l RateLimit) *ipLimiter {
	return &ipLimiter{limit: l, now: time.Now, clients: map[netip.Addr]*client{}}
}

// allow reports whether the request may proceed and, if not, how long to
// wait before retrying.
func (l *ipLimiter) allow(ip netip.Addr) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	c, ok := l.clients[ip]
	if !ok {
		if len(l.clients) >= maxTrackedIPs {
			l.evictIdleLocked(now)
		}
		if len(l.clients) >= maxTrackedIPs {
			// Tens of thousands of distinct IPs: that is a DDoS, which must
			// be handled upstream. Shed load instead of growing memory.
			return false, time.Minute
		}
		c = &client{lim: rate.NewLimiter(rate.Every(l.limit.Every), l.limit.Burst)}
		l.clients[ip] = c
	}
	c.lastSeen = now

	r := c.lim.ReserveN(now, 1)
	if delay := r.DelayFrom(now); delay > 0 {
		r.CancelAt(now) // do not consume a token for a rejected request
		return false, delay
	}
	return true, 0
}

func (l *ipLimiter) evictIdleLocked(now time.Time) {
	for ip, c := range l.clients {
		if now.Sub(c.lastSeen) > idleAfter {
			delete(l.clients, ip)
		}
	}
}

// cleanup periodically drops idle clients until ctx is done.
func (l *ipLimiter) cleanup(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.mu.Lock()
			l.evictIdleLocked(l.now())
			l.mu.Unlock()
		}
	}
}

// middleware answers 429 Too Many Requests with Retry-After when the client
// exceeds its budget.
func (l *ipLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := ClientIPFrom(r.Context())
		ok, wait := l.allow(ip)
		if !ok {
			secs := int(math.Ceil(wait.Seconds()))
			w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
			logFrom(r.Context()).WarnContext(r.Context(), "rate limited", "client_ip", ip.String(), "path", r.URL.Path)
			writeError(w, r, http.StatusTooManyRequests, "rate_limited", "too many requests, slow down")
			return
		}
		next.ServeHTTP(w, r)
	})
}
