package calendar

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AndreyZubov/family-calendar/backend/internal/store"
	"github.com/AndreyZubov/family-calendar/backend/internal/store/db"
)

// fixture is a service on a real database. Everything a test creates is
// deleted in Cleanup (the service commits its own transactions, so the
// rollback trick from the store tests does not apply here).
type fixture struct {
	t   *testing.T
	ctx context.Context
	st  *store.Store
	svc *Service

	users    []uuid.UUID
	families []uuid.UUID
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := store.New(ctx, store.Config{URL: url})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, ctx: ctx, st: st, svc: NewService(st)}
	t.Cleanup(func() {
		conn, err := pgx.Connect(ctx, url)
		if err != nil {
			t.Errorf("cleanup connect: %v", err)
			return
		}
		defer conn.Close(ctx)
		// Families first: cascades to members, events, participants, invites.
		if _, err := conn.Exec(ctx, "DELETE FROM families WHERE id = ANY($1)", f.families); err != nil {
			t.Errorf("cleanup families: %v", err)
		}
		if _, err := conn.Exec(ctx, "DELETE FROM users WHERE id = ANY($1)", f.users); err != nil {
			t.Errorf("cleanup users: %v", err)
		}
		st.Close()
	})
	return f
}

func (f *fixture) user(name string) uuid.UUID {
	f.t.Helper()
	u, err := f.st.UpsertUserByOIDC(f.ctx, db.UpsertUserByOIDCParams{
		OidcIssuer: "https://issuer.test", OidcSubject: uuid.NewString(),
		Email: name + "@example.com", Name: name,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	f.users = append(f.users, u.ID)
	return u.ID
}

func (f *fixture) family(owner uuid.UUID, name string) Member {
	f.t.Helper()
	m, err := f.svc.CreateFamily(f.ctx, owner, name)
	if err != nil {
		f.t.Fatal(err)
	}
	f.families = append(f.families, m.FamilyID)
	return m
}

func (f *fixture) join(owner Member, userID uuid.UUID) Member {
	f.t.Helper()
	inv, err := f.svc.CreateInvite(f.ctx, owner)
	if err != nil {
		f.t.Fatal(err)
	}
	m, err := f.svc.AcceptInvite(f.ctx, userID, inv.Token)
	if err != nil {
		f.t.Fatal(err)
	}
	return m
}

func (f *fixture) event(m Member, title string, start time.Time, participants ...uuid.UUID) Event {
	f.t.Helper()
	ev, err := f.svc.CreateEvent(f.ctx, m, EventInput{Title: title, StartsAt: start, EndsAt: start.Add(time.Hour), ParticipantIDs: participants})
	if err != nil {
		f.t.Fatal(err)
	}
	return ev
}

func participantIDs(ev Event) []uuid.UUID {
	ids := make([]uuid.UUID, len(ev.Participants))
	for i, p := range ev.Participants {
		ids[i] = p.UserID
	}
	return ids
}

func TestFamilyLifecycle(t *testing.T) {
	f := newFixture(t)
	alice := f.user("alice")

	if _, err := f.svc.Membership(f.ctx, alice); !errors.Is(err, ErrNoFamily) {
		t.Fatalf("new user: err = %v, want ErrNoFamily", err)
	}

	owner := f.family(alice, "  Ivanovs ")
	if owner.FamilyName != "Ivanovs" || !owner.IsOwner() {
		t.Errorf("owner = %+v", owner)
	}
	if _, err := f.svc.CreateFamily(f.ctx, alice, "Second"); !errors.Is(err, ErrAlreadyInFamily) {
		t.Errorf("second family: err = %v", err)
	}

	got, err := f.svc.Membership(f.ctx, alice)
	if err != nil || got != owner {
		t.Errorf("Membership = %+v, %v; want %+v", got, err, owner)
	}
}

func TestInvites(t *testing.T) {
	f := newFixture(t)
	alice, bob, carol := f.user("alice"), f.user("bob"), f.user("carol")
	owner := f.family(alice, "Ivanovs")

	inv, err := f.svc.CreateInvite(f.ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if !inv.ExpiresAt.After(time.Now().Add(6 * 24 * time.Hour)) {
		t.Errorf("invite expires too soon: %v", inv.ExpiresAt)
	}

	m, err := f.svc.AcceptInvite(f.ctx, bob, inv.Token)
	if err != nil || m.FamilyID != owner.FamilyID || m.Role != RoleMember || m.FamilyName != "Ivanovs" {
		t.Fatalf("accept: %+v, %v", m, err)
	}

	// Single use.
	if _, err := f.svc.AcceptInvite(f.ctx, carol, inv.Token); !errors.Is(err, ErrInviteInvalid) {
		t.Errorf("reused invite: err = %v", err)
	}
	// Garbage and unknown tokens.
	for _, tok := range []string{"", "nope", newToken()} {
		if _, err := f.svc.AcceptInvite(f.ctx, carol, tok); !errors.Is(err, ErrInviteInvalid) {
			t.Errorf("token %q: err = %v", tok, err)
		}
	}

	// Someone already in a family cannot accept, and the invite is not burned.
	inv2, _ := f.svc.CreateInvite(f.ctx, owner)
	if _, err := f.svc.AcceptInvite(f.ctx, bob, inv2.Token); !errors.Is(err, ErrAlreadyInFamily) {
		t.Errorf("member accepting again: err = %v", err)
	}
	if _, err := f.svc.AcceptInvite(f.ctx, carol, inv2.Token); err != nil {
		t.Errorf("invite was burned by a rejected accept: %v", err)
	}

	members, err := f.svc.ListMembers(f.ctx, owner)
	if err != nil || len(members) != 3 || members[0].UserID != alice || members[0].Role != RoleOwner {
		t.Errorf("members = %+v, %v", members, err)
	}
}

func TestExpiredInvite(t *testing.T) {
	f := newFixture(t)
	owner := f.family(f.user("alice"), "Ivanovs")
	f.svc.now = func() time.Time { return time.Now().Add(-InviteTTL - time.Minute) }
	inv, err := f.svc.CreateInvite(f.ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AcceptInvite(f.ctx, f.user("bob"), inv.Token); !errors.Is(err, ErrInviteInvalid) {
		t.Errorf("expired invite: err = %v", err)
	}
}

func TestRemoveMemberRules(t *testing.T) {
	f := newFixture(t)
	alice, bob, carol := f.user("alice"), f.user("bob"), f.user("carol")
	owner := f.family(alice, "Ivanovs")
	bobM := f.join(owner, bob)
	f.join(owner, carol)

	if err := f.svc.RemoveMember(f.ctx, owner, alice); !errors.Is(err, ErrOwnerCannotLeave) {
		t.Errorf("owner leaving: %v", err)
	}
	if err := f.svc.RemoveMember(f.ctx, bobM, carol); !errors.Is(err, ErrForbidden) {
		t.Errorf("member removing another: %v", err)
	}

	// Bob is a participant; leaving removes him from events too.
	ev := f.event(owner, "Dinner", t0, alice, bob)
	if err := f.svc.RemoveMember(f.ctx, bobM, bob); err != nil {
		t.Fatalf("member leaving: %v", err)
	}
	got, _ := f.svc.GetEvent(f.ctx, owner, ev.ID)
	if len(got.Participants) != 1 || got.Participants[0].UserID != alice {
		t.Errorf("participants after leave: %+v", got.Participants)
	}

	if err := f.svc.RemoveMember(f.ctx, owner, carol); err != nil {
		t.Errorf("owner removing member: %v", err)
	}
	if err := f.svc.RemoveMember(f.ctx, owner, carol); !errors.Is(err, ErrNotFound) {
		t.Errorf("removing twice: %v", err)
	}
}

func TestEventCRUD(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.user("alice"), f.user("bob")
	owner := f.family(alice, "Ivanovs")
	f.join(owner, bob)

	ev, err := f.svc.CreateEvent(f.ctx, owner, EventInput{
		Title: " Dinner ", Description: "at grandma's", StartsAt: t0, EndsAt: t0.Add(2 * time.Hour),
		ParticipantIDs: []uuid.UUID{bob, alice, bob},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Title != "Dinner" || *ev.CreatedBy != alice || len(ev.Participants) != 2 {
		t.Errorf("created = %+v", ev)
	}

	got, err := f.svc.GetEvent(f.ctx, owner, ev.ID)
	if err != nil || got.ID != ev.ID || len(got.Participants) != 2 {
		t.Errorf("get = %+v, %v", got, err)
	}

	// Update fields, keep participants (nil).
	upd, err := f.svc.UpdateEvent(f.ctx, owner, ev.ID, EventInput{Title: "Lunch", StartsAt: t0, EndsAt: t0.Add(time.Hour)})
	if err != nil || upd.Title != "Lunch" || len(upd.Participants) != 2 || !upd.UpdatedAt.After(ev.UpdatedAt) && !upd.UpdatedAt.Equal(ev.UpdatedAt) {
		t.Errorf("update keep participants: %+v, %v", upd, err)
	}

	// Replace participants: only bob.
	upd, err = f.svc.UpdateEvent(f.ctx, owner, ev.ID, EventInput{Title: "Lunch", StartsAt: t0, EndsAt: t0, ParticipantIDs: []uuid.UUID{bob}})
	if err != nil || len(upd.Participants) != 1 || upd.Participants[0].UserID != bob {
		t.Errorf("update replace participants: %+v, %v", participantIDs(upd), err)
	}

	// Empty list removes all.
	upd, err = f.svc.UpdateEvent(f.ctx, owner, ev.ID, EventInput{Title: "Lunch", StartsAt: t0, EndsAt: t0, ParticipantIDs: []uuid.UUID{}})
	if err != nil || len(upd.Participants) != 0 {
		t.Errorf("update clear participants: %+v, %v", participantIDs(upd), err)
	}

	if err := f.svc.DeleteEvent(f.ctx, owner, ev.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.GetEvent(f.ctx, owner, ev.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete: %v", err)
	}
	if err := f.svc.DeleteEvent(f.ctx, owner, ev.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
}

func TestParticipants(t *testing.T) {
	f := newFixture(t)
	alice, bob, stranger := f.user("alice"), f.user("bob"), f.user("stranger")
	owner := f.family(alice, "Ivanovs")
	f.join(owner, bob)
	f.family(stranger, "Petrovs")
	ev := f.event(owner, "Walk", t0)

	if err := f.svc.AddParticipant(f.ctx, owner, ev.ID, bob); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.AddParticipant(f.ctx, owner, ev.ID, bob); err != nil {
		t.Errorf("adding twice must succeed: %v", err)
	}

	// Not a family member: rejected by the database's composite FK.
	err := f.svc.AddParticipant(f.ctx, owner, ev.ID, stranger)
	if fields := fieldsOf(t, err); fields["participantIds"] == "" {
		t.Errorf("stranger as participant: %v", err)
	}
	// Same through create: the whole event is rolled back.
	_, err = f.svc.CreateEvent(f.ctx, owner, EventInput{Title: "x", StartsAt: t0, EndsAt: t0, ParticipantIDs: []uuid.UUID{bob, stranger}})
	if _, ok := IsValidation(err); !ok {
		t.Errorf("create with stranger: %v", err)
	}
	if evs, _ := f.svc.ListEvents(f.ctx, owner, t0.Add(-time.Hour), t0.Add(time.Hour)); len(evs) != 1 {
		t.Errorf("failed create left an event behind: %d events", len(evs))
	}

	if err := f.svc.AddParticipant(f.ctx, owner, uuid.New(), bob); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown event: %v", err)
	}
	if err := f.svc.RemoveParticipant(f.ctx, owner, ev.ID, bob); err != nil {
		t.Errorf("remove: %v", err)
	}
	if err := f.svc.RemoveParticipant(f.ctx, owner, ev.ID, bob); !errors.Is(err, ErrNotFound) {
		t.Errorf("remove twice: %v", err)
	}
}

func TestFamiliesAreIsolated(t *testing.T) {
	f := newFixture(t)
	ivanovs := f.family(f.user("alice"), "Ivanovs")
	petrovs := f.family(f.user("petr"), "Petrovs")
	ev := f.event(ivanovs, "Private", t0)

	if _, err := f.svc.GetEvent(f.ctx, petrovs, ev.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("get foreign event: %v", err)
	}
	if _, err := f.svc.UpdateEvent(f.ctx, petrovs, ev.ID, EventInput{Title: "hacked", StartsAt: t0, EndsAt: t0}); !errors.Is(err, ErrNotFound) {
		t.Errorf("update foreign event: %v", err)
	}
	if err := f.svc.DeleteEvent(f.ctx, petrovs, ev.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete foreign event: %v", err)
	}
	if err := f.svc.AddParticipant(f.ctx, petrovs, ev.ID, petrovs.UserID); !errors.Is(err, ErrNotFound) {
		t.Errorf("add participant to foreign event: %v", err)
	}
	evs, _ := f.svc.ListEvents(f.ctx, petrovs, t0.Add(-time.Hour), t0.Add(time.Hour))
	if len(evs) != 0 {
		t.Errorf("foreign family sees %d events", len(evs))
	}
	if got, _ := f.svc.GetEvent(f.ctx, ivanovs, ev.ID); got.Title != "Private" {
		t.Error("event was modified by another family")
	}
}

func TestListEvents(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.user("alice"), f.user("bob")
	m := f.family(alice, "Ivanovs")
	f.join(m, bob)

	day := 24 * time.Hour
	later := f.event(m, "Later", t0.Add(2*day), bob)
	early := f.event(m, "Early", t0, alice, bob)
	f.event(m, "Outside", t0.Add(40*day))

	evs, err := f.svc.ListEvents(f.ctx, m, t0.Add(-day), t0.Add(7*day))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 || evs[0].ID != early.ID || evs[1].ID != later.ID {
		t.Fatalf("got %d events, want [Early, Later] in order", len(evs))
	}
	if len(evs[0].Participants) != 2 || len(evs[1].Participants) != 1 {
		t.Errorf("participants not attached: %v / %v", participantIDs(evs[0]), participantIDs(evs[1]))
	}

	if _, err := f.svc.ListEvents(f.ctx, m, t0, t0); err == nil {
		t.Error("empty range must be rejected")
	}
}
