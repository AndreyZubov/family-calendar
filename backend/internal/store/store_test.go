package store

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AndreyZubov/family-calendar/backend/internal/store/db"
	"github.com/AndreyZubov/family-calendar/backend/internal/testdb"
)

// openTestStore connects to a fresh, migrated database of its own (see
// package testdb), so tests run in parallel without seeing each other's data.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	t.Parallel()
	s, err := New(context.Background(), Config{URL: testdb.New(t), MaxConns: 2})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

// inRolledBackTx gives fn queries bound to a transaction that is always
// rolled back, so tests leave no data behind.
func inRolledBackTx(t *testing.T, s *Store, fn func(ctx context.Context, q *db.Queries)) {
	t.Helper()
	ctx := context.Background()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	fn(ctx, s.Queries.WithTx(tx))
}

func mustUser(ctx context.Context, t *testing.T, q *db.Queries, sub string) db.User {
	t.Helper()
	u, err := q.UpsertUserByOIDC(ctx, db.UpsertUserByOIDCParams{
		OidcIssuer: "https://issuer.test", OidcSubject: sub,
		Email: sub + "@example.com", Name: "User " + sub,
	})
	if err != nil {
		t.Fatalf("UpsertUserByOIDC(%s): %v", sub, err)
	}
	return u
}

func mustFamily(ctx context.Context, t *testing.T, q *db.Queries, owner db.User) db.Family {
	t.Helper()
	f, err := q.CreateFamily(ctx, "Family of "+owner.Name)
	if err != nil {
		t.Fatalf("CreateFamily: %v", err)
	}
	if err := q.AddFamilyMember(ctx, db.AddFamilyMemberParams{FamilyID: f.ID, UserID: owner.ID, Role: "owner"}); err != nil {
		t.Fatalf("AddFamilyMember: %v", err)
	}
	return f
}

func TestUpsertUserIsIdempotent(t *testing.T) {
	s := openTestStore(t)
	inRolledBackTx(t, s, func(ctx context.Context, q *db.Queries) {
		first := mustUser(ctx, t, q, "alice")
		again, err := q.UpsertUserByOIDC(ctx, db.UpsertUserByOIDCParams{
			OidcIssuer: "https://issuer.test", OidcSubject: "alice",
			Email: "new@example.com", Name: "Alice Renamed",
		})
		if err != nil {
			t.Fatal(err)
		}
		if again.ID != first.ID {
			t.Errorf("second login created a new user: %v != %v", again.ID, first.ID)
		}
		if again.Email != "new@example.com" || again.Name != "Alice Renamed" {
			t.Errorf("profile not refreshed: %+v", again)
		}
	})
}

func TestEventsAreScopedToFamily(t *testing.T) {
	s := openTestStore(t)
	inRolledBackTx(t, s, func(ctx context.Context, q *db.Queries) {
		alice := mustUser(ctx, t, q, "alice")
		bob := mustUser(ctx, t, q, "bob") // lives in another family
		famA := mustFamily(ctx, t, q, alice)
		famB := mustFamily(ctx, t, q, bob)

		start := time.Date(2026, 11, 10, 18, 0, 0, 0, time.UTC)
		ev, err := q.CreateEvent(ctx, db.CreateEventParams{
			FamilyID: famA.ID, Title: "Dinner",
			StartsAt: start, EndsAt: start.Add(time.Hour), CreatedBy: &alice.ID,
		})
		if err != nil {
			t.Fatal(err)
		}

		// Another family cannot see, update or delete the event.
		if _, err := q.GetEvent(ctx, db.GetEventParams{ID: ev.ID, FamilyID: famB.ID}); !IsNotFound(err) {
			t.Errorf("GetEvent from other family: want not found, got %v", err)
		}
		if n, err := q.DeleteEvent(ctx, db.DeleteEventParams{ID: ev.ID, FamilyID: famB.ID}); err != nil || n != 0 {
			t.Errorf("DeleteEvent from other family: n=%d err=%v", n, err)
		}

		// Range query uses overlap semantics on [from, to).
		cases := []struct {
			name     string
			from, to time.Time
			want     int
		}{
			{"window contains event", start.Add(-time.Hour), start.Add(2 * time.Hour), 1},
			{"window overlaps end", start.Add(30 * time.Minute), start.Add(2 * time.Hour), 1},
			{"window ends at start", start.Add(-time.Hour), start, 0},
			{"window starts at end", start.Add(time.Hour), start.Add(2 * time.Hour), 0},
		}
		for _, c := range cases {
			got, err := q.ListEventsInRange(ctx, db.ListEventsInRangeParams{FamilyID: famA.ID, RangeFrom: c.from, RangeTo: c.to})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != c.want {
				t.Errorf("%s: got %d events, want %d", c.name, len(got), c.want)
			}
		}
	})
}

func TestParticipantsMustBeFamilyMembers(t *testing.T) {
	s := openTestStore(t)
	inRolledBackTx(t, s, func(ctx context.Context, q *db.Queries) {
		alice := mustUser(ctx, t, q, "alice")
		bob := mustUser(ctx, t, q, "bob")
		famA := mustFamily(ctx, t, q, alice)
		_ = mustFamily(ctx, t, q, bob)

		start := time.Now().UTC()
		ev, err := q.CreateEvent(ctx, db.CreateEventParams{FamilyID: famA.ID, Title: "Walk", StartsAt: start, EndsAt: start})
		if err != nil {
			t.Fatal(err)
		}

		n, err := q.AddEventParticipant(ctx, db.AddEventParticipantParams{EventID: ev.ID, FamilyID: famA.ID, UserID: alice.ID})
		if err != nil || n != 1 {
			t.Fatalf("add member: n=%d err=%v", n, err)
		}
		n, err = q.AddEventParticipant(ctx, db.AddEventParticipantParams{EventID: ev.ID, FamilyID: famA.ID, UserID: alice.ID})
		if err != nil || n != 0 {
			t.Errorf("re-adding member should be a no-op: n=%d err=%v", n, err)
		}

		parts, err := q.ListParticipantsForEvents(ctx, db.ListParticipantsForEventsParams{FamilyID: famA.ID, EventIds: []uuid.UUID{ev.ID}})
		if err != nil || len(parts) != 1 || parts[0].UserID != alice.ID {
			t.Errorf("ListParticipantsForEvents: %+v err=%v", parts, err)
		}

		// Run the failing insert last: an error aborts the transaction.
		_, err = q.AddEventParticipant(ctx, db.AddEventParticipantParams{EventID: ev.ID, FamilyID: famA.ID, UserID: bob.ID})
		if !IsForeignKeyViolation(err) {
			t.Errorf("add non-member: want FK violation, got %v", err)
		}
	})
}

func TestSessionLifecycle(t *testing.T) {
	s := openTestStore(t)
	inRolledBackTx(t, s, func(ctx context.Context, q *db.Queries) {
		alice := mustUser(ctx, t, q, "alice")
		live := sha256.Sum256([]byte("live-token"))
		expired := sha256.Sum256([]byte("expired-token"))

		for _, p := range []db.CreateSessionParams{
			{TokenHash: live[:], UserID: alice.ID, ExpiresAt: time.Now().Add(time.Hour)},
			{TokenHash: expired[:], UserID: alice.ID, ExpiresAt: time.Now().Add(-time.Minute)},
		} {
			if err := q.CreateSession(ctx, p); err != nil {
				t.Fatal(err)
			}
		}

		row, err := q.GetSessionUser(ctx, live[:])
		if err != nil || row.User.ID != alice.ID {
			t.Fatalf("GetSessionUser(live): %+v err=%v", row, err)
		}
		if _, err := q.GetSessionUser(ctx, expired[:]); !IsNotFound(err) {
			t.Errorf("GetSessionUser(expired): want not found, got %v", err)
		}
		if n, err := q.DeleteExpiredSessions(ctx); err != nil || n < 1 {
			t.Errorf("DeleteExpiredSessions: n=%d err=%v", n, err)
		}
	})
}

func TestInviteIsSingleUse(t *testing.T) {
	s := openTestStore(t)
	inRolledBackTx(t, s, func(ctx context.Context, q *db.Queries) {
		alice := mustUser(ctx, t, q, "alice")
		bob := mustUser(ctx, t, q, "bob")
		fam := mustFamily(ctx, t, q, alice)
		token := sha256.Sum256([]byte("invite-token"))

		if _, err := q.CreateInvite(ctx, db.CreateInviteParams{
			FamilyID: fam.ID, TokenHash: token[:], CreatedBy: &alice.ID, ExpiresAt: time.Now().Add(24 * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}

		famID, err := q.ConsumeInvite(ctx, db.ConsumeInviteParams{TokenHash: token[:], UsedBy: &bob.ID})
		if err != nil || famID != fam.ID {
			t.Fatalf("first ConsumeInvite: %v err=%v", famID, err)
		}
		if _, err := q.ConsumeInvite(ctx, db.ConsumeInviteParams{TokenHash: token[:], UsedBy: &bob.ID}); !IsNotFound(err) {
			t.Errorf("second ConsumeInvite: want not found, got %v", err)
		}
	})
}

func TestInTxRollsBackOnError(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	sub := "rollback-" + uuid.NewString()

	err := s.InTx(ctx, func(q *db.Queries) error {
		mustUser(ctx, t, q, sub)
		return context.Canceled // any error
	})
	if err == nil {
		t.Fatal("InTx: want error")
	}

	var n int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE oidc_subject = $1", sub).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("user was committed despite error")
	}
}

func TestMigrateAndPing(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	// The database is already migrated: Migrate must be a no-op.
	if err := s.Migrate(ctx, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
}

func TestErrorClassification(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// CHECK: family name must not be empty.
	_, err := s.CreateFamily(ctx, "")
	if !IsCheckViolation(err) || IsUniqueViolation(err) || IsForeignKeyViolation(err) {
		t.Errorf("empty family name: %v", err)
	}

	// UNIQUE: the same membership twice.
	err = s.InTx(ctx, func(q *db.Queries) error {
		u := mustUser(ctx, t, q, "alice")
		f := mustFamily(ctx, t, q, u)
		return q.AddFamilyMember(ctx, db.AddFamilyMemberParams{FamilyID: f.ID, UserID: u.ID, Role: "member"})
	})
	if !IsUniqueViolation(err) || IsCheckViolation(err) {
		t.Errorf("duplicate membership: %v", err)
	}

	// Not found.
	if _, err := s.GetUserByID(ctx, uuid.New()); !IsNotFound(err) {
		t.Errorf("unknown user: %v", err)
	}
	// Unrelated errors are not misclassified.
	if IsNotFound(nil) || IsUniqueViolation(context.Canceled) {
		t.Error("misclassified non-database error")
	}
}
