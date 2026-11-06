package auth

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AndreyZubov/family-calendar/backend/internal/store/db"
)

// memSessions is an in-memory SessionQueries mirroring the SQL semantics.
type memSessions struct {
	now  func() time.Time
	rows map[string]db.Session
}

func newMemSessions(now func() time.Time) *memSessions {
	return &memSessions{now: now, rows: map[string]db.Session{}}
}

func (m *memSessions) CreateSession(_ context.Context, a db.CreateSessionParams) error {
	n := m.now()
	m.rows[string(a.TokenHash)] = db.Session{TokenHash: a.TokenHash, UserID: a.UserID, CreatedAt: n, LastSeenAt: n, ExpiresAt: a.ExpiresAt}
	return nil
}

func (m *memSessions) GetSessionUser(_ context.Context, h []byte) (db.GetSessionUserRow, error) {
	s, ok := m.rows[string(h)]
	if !ok || !s.ExpiresAt.After(m.now()) {
		return db.GetSessionUserRow{}, pgx.ErrNoRows
	}
	return db.GetSessionUserRow{User: db.User{ID: s.UserID}, SessionExpiresAt: s.ExpiresAt, SessionLastSeenAt: s.LastSeenAt}, nil
}

func (m *memSessions) TouchSession(_ context.Context, a db.TouchSessionParams) error {
	s := m.rows[string(a.TokenHash)]
	s.LastSeenAt, s.ExpiresAt = m.now(), a.ExpiresAt
	m.rows[string(a.TokenHash)] = s
	return nil
}

func (m *memSessions) DeleteSession(_ context.Context, h []byte) error {
	delete(m.rows, string(h))
	return nil
}

func (m *memSessions) DeleteExpiredSessions(context.Context) (int64, error) {
	var n int64
	for k, s := range m.rows {
		if !s.ExpiresAt.After(m.now()) {
			delete(m.rows, k)
			n++
		}
	}
	return n, nil
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestSessions() (*Sessions, *memSessions, *clock) {
	c := &clock{t: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)}
	q := newMemSessions(c.now)
	s := NewSessions(q, 24*time.Hour)
	s.now = c.now
	return s, q, c
}

func TestSessionCreateAndLookup(t *testing.T) {
	s, q, _ := newTestSessions()
	ctx := context.Background()
	user := uuid.New()

	token, _, err := s.Create(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	for h := range q.rows {
		if bytes.Contains([]byte(h), []byte(token)) {
			t.Fatal("raw token stored in the database")
		}
	}

	sess, err := s.Lookup(ctx, token)
	if err != nil || sess.User.ID != user || sess.Renewed {
		t.Fatalf("Lookup = %+v, %v", sess, err)
	}
}

func TestSessionSlidingExpiration(t *testing.T) {
	s, _, c := newTestSessions()
	ctx := context.Background()
	token, firstExpiry, _ := s.Create(ctx, uuid.New())

	// Within touchEvery: no database write, expiry unchanged.
	c.advance(30 * time.Minute)
	sess, _ := s.Lookup(ctx, token)
	if sess.Renewed || !sess.ExpiresAt.Equal(firstExpiry) {
		t.Errorf("renewed too early: %+v", sess)
	}

	// After touchEvery: expiry moves forward.
	c.advance(time.Hour)
	sess, _ = s.Lookup(ctx, token)
	if !sess.Renewed || !sess.ExpiresAt.Equal(c.now().Add(24*time.Hour)) {
		t.Errorf("not renewed: %+v", sess)
	}

	// Active users stay signed in past the original expiry.
	for range 3 {
		c.advance(20 * time.Hour)
		if _, err := s.Lookup(ctx, token); err != nil {
			t.Fatalf("active session expired: %v", err)
		}
	}

	// Idle longer than TTL: gone.
	c.advance(25 * time.Hour)
	if _, err := s.Lookup(ctx, token); err != ErrNoSession {
		t.Errorf("idle session: err = %v, want ErrNoSession", err)
	}
}

func TestSessionDestroy(t *testing.T) {
	s, _, _ := newTestSessions()
	ctx := context.Background()
	token, _, _ := s.Create(ctx, uuid.New())

	if err := s.Destroy(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(ctx, token); err != ErrNoSession {
		t.Errorf("after Destroy: err = %v", err)
	}
	if err := s.Destroy(ctx, "garbage"); err != nil {
		t.Errorf("Destroy(garbage) = %v", err)
	}
}

func TestSessionLookupRejectsMalformedTokens(t *testing.T) {
	s, _, _ := newTestSessions()
	for _, tok := range []string{"", "short", "!!!!", randomToken() + "x"} {
		if _, err := s.Lookup(context.Background(), tok); err != ErrNoSession {
			t.Errorf("Lookup(%q) = %v", tok, err)
		}
	}
}

func TestDeleteExpired(t *testing.T) {
	s, q, c := newTestSessions()
	ctx := context.Background()
	_, _, _ = s.Create(ctx, uuid.New())
	c.advance(25 * time.Hour)
	_, _, _ = s.Create(ctx, uuid.New()) // still valid

	n, err := s.DeleteExpired(ctx)
	if err != nil || n != 1 || len(q.rows) != 1 {
		t.Errorf("DeleteExpired: n=%d err=%v left=%d", n, err, len(q.rows))
	}
}
