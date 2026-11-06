package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AndreyZubov/family-calendar/backend/internal/store/db"
)

// SessionQueries are the database queries sessions need.
type SessionQueries interface {
	CreateSession(ctx context.Context, arg db.CreateSessionParams) error
	GetSessionUser(ctx context.Context, tokenHash []byte) (db.GetSessionUserRow, error)
	TouchSession(ctx context.Context, arg db.TouchSessionParams) error
	DeleteSession(ctx context.Context, tokenHash []byte) error
	DeleteExpiredSessions(ctx context.Context) (int64, error)
}

// Sessions manages server-side sessions. The browser holds a random token;
// the database stores only its SHA-256 hash, so a leaked database cannot be
// used to hijack sessions.
type Sessions struct {
	q   SessionQueries
	ttl time.Duration
	// touchEvery limits writes: an active session's expiry is extended at
	// most this often instead of on every request.
	touchEvery time.Duration
	now        func() time.Time
}

// NewSessions returns a session manager with sliding expiration: a session
// expires after ttl without activity.
func NewSessions(q SessionQueries, ttl time.Duration) *Sessions {
	return &Sessions{q: q, ttl: ttl, touchEvery: time.Hour, now: time.Now}
}

// TTL is the idle lifetime of a session.
func (s *Sessions) TTL() time.Duration { return s.ttl }

// Session is an authenticated session resolved from a cookie.
type Session struct {
	User      db.User
	ExpiresAt time.Time
	// Renewed is true when the expiry was just extended; the caller should
	// re-send the cookie with the new Max-Age.
	Renewed bool
}

// ErrNoSession means the token is unknown or the session expired.
var ErrNoSession = errors.New("no valid session")

// Create starts a session for the user and returns the token for the cookie.
func (s *Sessions) Create(ctx context.Context, userID uuid.UUID) (token string, expires time.Time, err error) {
	token = randomToken()
	expires = s.now().Add(s.ttl)
	if err := s.q.CreateSession(ctx, db.CreateSessionParams{
		TokenHash: hashToken(token),
		UserID:    userID,
		ExpiresAt: expires,
	}); err != nil {
		return "", time.Time{}, fmt.Errorf("create session: %w", err)
	}
	return token, expires, nil
}

// Lookup resolves a cookie token to its session, extending it if due.
func (s *Sessions) Lookup(ctx context.Context, token string) (Session, error) {
	if !validTokenFormat(token) {
		return Session{}, ErrNoSession // skip the query for garbage cookies
	}
	h := hashToken(token)
	row, err := s.q.GetSessionUser(ctx, h)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNoSession
	}
	if err != nil {
		return Session{}, fmt.Errorf("lookup session: %w", err)
	}

	sess := Session{User: row.User, ExpiresAt: row.SessionExpiresAt}
	now := s.now()
	if now.Sub(row.SessionLastSeenAt) >= s.touchEvery {
		sess.ExpiresAt = now.Add(s.ttl)
		if err := s.q.TouchSession(ctx, db.TouchSessionParams{TokenHash: h, ExpiresAt: sess.ExpiresAt}); err != nil {
			return Session{}, fmt.Errorf("extend session: %w", err)
		}
		sess.Renewed = true
	}
	return sess, nil
}

// Destroy deletes the session (logout). Unknown tokens are not an error.
func (s *Sessions) Destroy(ctx context.Context, token string) error {
	if !validTokenFormat(token) {
		return nil
	}
	return s.q.DeleteSession(ctx, hashToken(token))
}

// DeleteExpired removes expired sessions; run periodically.
func (s *Sessions) DeleteExpired(ctx context.Context) (int64, error) {
	return s.q.DeleteExpiredSessions(ctx)
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

func validTokenFormat(token string) bool {
	b, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(b) == 32
}
