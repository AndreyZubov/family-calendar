package calendar

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/AndreyZubov/family-calendar/backend/internal/store"
	"github.com/AndreyZubov/family-calendar/backend/internal/store/db"
)

// Store is the database access the service needs: the generated queries
// plus transactions. *store.Store implements it.
type Store interface {
	db.Querier
	InTx(ctx context.Context, fn func(q *db.Queries) error) error
}

// Service implements the calendar use cases.
type Service struct {
	st  Store
	now func() time.Time
}

// NewService returns a calendar service backed by st.
func NewService(st Store) *Service {
	return &Service{st: st, now: time.Now}
}

// ---------------------------------------------------------------------------
// Family

// Membership returns the user's family membership, or ErrNoFamily.
func (s *Service) Membership(ctx context.Context, userID uuid.UUID) (Member, error) {
	row, err := s.st.GetMembershipByUser(ctx, userID)
	if store.IsNotFound(err) {
		return Member{}, ErrNoFamily
	}
	if err != nil {
		return Member{}, fmt.Errorf("get membership: %w", err)
	}
	return Member{UserID: userID, FamilyID: row.FamilyID, FamilyName: row.FamilyName, Role: row.Role}, nil
}

// CreateFamily creates a family with the user as its owner.
func (s *Service) CreateFamily(ctx context.Context, userID uuid.UUID, name string) (Member, error) {
	name, err := validateFamilyName(name)
	if err != nil {
		return Member{}, err
	}

	var m Member
	err = s.st.InTx(ctx, func(q *db.Queries) error {
		if _, err := q.GetMembershipByUser(ctx, userID); err == nil {
			return ErrAlreadyInFamily
		} else if !store.IsNotFound(err) {
			return err
		}
		f, err := q.CreateFamily(ctx, name)
		if err != nil {
			return err
		}
		if err := q.AddFamilyMember(ctx, db.AddFamilyMemberParams{FamilyID: f.ID, UserID: userID, Role: RoleOwner}); err != nil {
			// Lost a race with a concurrent create/accept: the unique index
			// on family_members(user_id) still allows only one family.
			if store.IsUniqueViolation(err) {
				return ErrAlreadyInFamily
			}
			return err
		}
		m = Member{UserID: userID, FamilyID: f.ID, FamilyName: f.Name, Role: RoleOwner}
		return nil
	})
	return m, err
}

// ListMembers returns everyone in the member's family.
func (s *Service) ListMembers(ctx context.Context, m Member) ([]FamilyMember, error) {
	rows, err := s.st.ListFamilyMembers(ctx, m.FamilyID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	out := make([]FamilyMember, len(rows))
	for i, r := range rows {
		out[i] = FamilyMember{UserID: r.ID, Email: r.Email, Name: r.Name, AvatarURL: r.AvatarUrl, Role: r.Role, JoinedAt: r.JoinedAt}
	}
	return out, nil
}

// RemoveMember removes a user from the family. Members may leave (remove
// themselves); only the owner may remove others; the owner cannot leave.
func (s *Service) RemoveMember(ctx context.Context, m Member, userID uuid.UUID) error {
	switch {
	case userID == m.UserID && m.IsOwner():
		return ErrOwnerCannotLeave
	case userID != m.UserID && !m.IsOwner():
		return ErrForbidden
	}
	n, err := s.st.RemoveFamilyMember(ctx, db.RemoveFamilyMemberParams{FamilyID: m.FamilyID, UserID: userID})
	if err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateInvite creates a single-use invitation to the member's family.
func (s *Service) CreateInvite(ctx context.Context, m Member) (Invite, error) {
	token := newToken()
	expires := s.now().Add(InviteTTL)
	if _, err := s.st.CreateInvite(ctx, db.CreateInviteParams{
		FamilyID: m.FamilyID, TokenHash: hashToken(token), CreatedBy: &m.UserID, ExpiresAt: expires,
	}); err != nil {
		return Invite{}, fmt.Errorf("create invite: %w", err)
	}
	return Invite{Token: token, ExpiresAt: expires}, nil
}

// AcceptInvite adds the user to the invite's family.
func (s *Service) AcceptInvite(ctx context.Context, userID uuid.UUID, token string) (Member, error) {
	if !validToken(token) {
		return Member{}, ErrInviteInvalid
	}
	var m Member
	err := s.st.InTx(ctx, func(q *db.Queries) error {
		if _, err := q.GetMembershipByUser(ctx, userID); err == nil {
			return ErrAlreadyInFamily // checked first so the invite is not burned
		} else if !store.IsNotFound(err) {
			return err
		}
		familyID, err := q.ConsumeInvite(ctx, db.ConsumeInviteParams{TokenHash: hashToken(token), UsedBy: &userID})
		if store.IsNotFound(err) {
			return ErrInviteInvalid
		}
		if err != nil {
			return err
		}
		if err := q.AddFamilyMember(ctx, db.AddFamilyMemberParams{FamilyID: familyID, UserID: userID, Role: RoleMember}); err != nil {
			if store.IsUniqueViolation(err) {
				return ErrAlreadyInFamily // rollback also restores the invite
			}
			return err
		}
		row, err := q.GetMembershipByUser(ctx, userID)
		if err != nil {
			return err
		}
		m = Member{UserID: userID, FamilyID: familyID, FamilyName: row.FamilyName, Role: RoleMember}
		return nil
	})
	return m, err
}

// ---------------------------------------------------------------------------
// Events

// ListEvents returns the family's events overlapping [from, to), with
// participants, ordered by start time.
func (s *Service) ListEvents(ctx context.Context, m Member, from, to time.Time) ([]Event, error) {
	if err := validateRange(from, to); err != nil {
		return nil, err
	}
	rows, err := s.st.ListEventsInRange(ctx, db.ListEventsInRangeParams{FamilyID: m.FamilyID, RangeFrom: from, RangeTo: to})
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return s.withParticipants(ctx, s.st, m.FamilyID, rows)
}

// GetEvent returns one event of the family.
func (s *Service) GetEvent(ctx context.Context, m Member, id uuid.UUID) (Event, error) {
	row, err := s.st.GetEvent(ctx, db.GetEventParams{ID: id, FamilyID: m.FamilyID})
	if store.IsNotFound(err) {
		return Event{}, ErrNotFound
	}
	if err != nil {
		return Event{}, fmt.Errorf("get event: %w", err)
	}
	evs, err := s.withParticipants(ctx, s.st, m.FamilyID, []db.Event{row})
	if err != nil {
		return Event{}, err
	}
	return evs[0], nil
}

// CreateEvent creates an event in the member's family.
func (s *Service) CreateEvent(ctx context.Context, m Member, in EventInput) (Event, error) {
	if err := in.normalize(); err != nil {
		return Event{}, err
	}
	var ev Event
	err := s.st.InTx(ctx, func(q *db.Queries) error {
		row, err := q.CreateEvent(ctx, db.CreateEventParams{
			FamilyID: m.FamilyID, Title: in.Title, Description: in.Description,
			StartsAt: in.StartsAt, EndsAt: in.EndsAt, AllDay: in.AllDay, CreatedBy: &m.UserID,
		})
		if err != nil {
			return err
		}
		if err := addParticipants(ctx, q, m.FamilyID, row.ID, in.ParticipantIDs); err != nil {
			return err
		}
		evs, err := s.withParticipants(ctx, q, m.FamilyID, []db.Event{row})
		if err != nil {
			return err
		}
		ev = evs[0]
		return nil
	})
	return ev, err
}

// UpdateEvent replaces the event's fields and, if ParticipantIDs is not nil,
// its participant list. Any family member may edit any family event.
func (s *Service) UpdateEvent(ctx context.Context, m Member, id uuid.UUID, in EventInput) (Event, error) {
	if err := in.normalize(); err != nil {
		return Event{}, err
	}
	var ev Event
	err := s.st.InTx(ctx, func(q *db.Queries) error {
		row, err := q.UpdateEvent(ctx, db.UpdateEventParams{
			ID: id, FamilyID: m.FamilyID, Title: in.Title, Description: in.Description,
			StartsAt: in.StartsAt, EndsAt: in.EndsAt, AllDay: in.AllDay,
		})
		if store.IsNotFound(err) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if in.ParticipantIDs != nil {
			if err := replaceParticipants(ctx, q, m.FamilyID, id, in.ParticipantIDs); err != nil {
				return err
			}
		}
		evs, err := s.withParticipants(ctx, q, m.FamilyID, []db.Event{row})
		if err != nil {
			return err
		}
		ev = evs[0]
		return nil
	})
	return ev, err
}

// DeleteEvent deletes an event of the family.
func (s *Service) DeleteEvent(ctx context.Context, m Member, id uuid.UUID) error {
	n, err := s.st.DeleteEvent(ctx, db.DeleteEventParams{ID: id, FamilyID: m.FamilyID})
	if err != nil {
		return fmt.Errorf("delete event: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddParticipant adds a family member to an event. Adding someone who
// already participates succeeds without changes.
func (s *Service) AddParticipant(ctx context.Context, m Member, eventID, userID uuid.UUID) error {
	if _, err := s.st.GetEvent(ctx, db.GetEventParams{ID: eventID, FamilyID: m.FamilyID}); store.IsNotFound(err) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("get event: %w", err)
	}
	return addParticipants(ctx, s.st, m.FamilyID, eventID, []uuid.UUID{userID})
}

// RemoveParticipant removes a user from an event.
func (s *Service) RemoveParticipant(ctx context.Context, m Member, eventID, userID uuid.UUID) error {
	n, err := s.st.RemoveEventParticipant(ctx, db.RemoveEventParticipantParams{EventID: eventID, FamilyID: m.FamilyID, UserID: userID})
	if err != nil {
		return fmt.Errorf("remove participant: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------------------------------------------------------------------------
// helpers

type participantQueries interface {
	AddEventParticipant(ctx context.Context, arg db.AddEventParticipantParams) (int64, error)
	RemoveEventParticipant(ctx context.Context, arg db.RemoveEventParticipantParams) (int64, error)
	ListParticipantsForEvents(ctx context.Context, arg db.ListParticipantsForEventsParams) ([]db.ListParticipantsForEventsRow, error)
}

// addParticipants relies on the composite foreign keys: a user who is not a
// member of the event's family is rejected by Postgres itself.
func addParticipants(ctx context.Context, q participantQueries, familyID, eventID uuid.UUID, userIDs []uuid.UUID) error {
	for _, uid := range userIDs {
		_, err := q.AddEventParticipant(ctx, db.AddEventParticipantParams{EventID: eventID, FamilyID: familyID, UserID: uid})
		if store.IsForeignKeyViolation(err) {
			return &ValidationError{Fields: map[string]string{"participantIds": "user " + uid.String() + " is not a member of this family"}}
		}
		if err != nil {
			return fmt.Errorf("add participant: %w", err)
		}
	}
	return nil
}

func replaceParticipants(ctx context.Context, q participantQueries, familyID, eventID uuid.UUID, want []uuid.UUID) error {
	current, err := q.ListParticipantsForEvents(ctx, db.ListParticipantsForEventsParams{FamilyID: familyID, EventIds: []uuid.UUID{eventID}})
	if err != nil {
		return fmt.Errorf("list participants: %w", err)
	}
	keep := make(map[uuid.UUID]bool, len(want))
	for _, id := range want {
		keep[id] = true
	}
	for _, p := range current {
		if !keep[p.UserID] {
			if _, err := q.RemoveEventParticipant(ctx, db.RemoveEventParticipantParams{EventID: eventID, FamilyID: familyID, UserID: p.UserID}); err != nil {
				return fmt.Errorf("remove participant: %w", err)
			}
		}
	}
	return addParticipants(ctx, q, familyID, eventID, want) // existing ones are no-ops
}

// withParticipants loads participants for all events in one query.
func (s *Service) withParticipants(ctx context.Context, q participantQueries, familyID uuid.UUID, rows []db.Event) ([]Event, error) {
	out := make([]Event, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	parts, err := q.ListParticipantsForEvents(ctx, db.ListParticipantsForEventsParams{FamilyID: familyID, EventIds: ids})
	if err != nil {
		return nil, fmt.Errorf("list participants: %w", err)
	}
	byEvent := make(map[uuid.UUID][]Participant, len(rows))
	for _, p := range parts {
		byEvent[p.EventID] = append(byEvent[p.EventID], Participant{UserID: p.UserID, Name: p.Name, AvatarURL: p.AvatarUrl})
	}
	for i, r := range rows {
		ps := byEvent[r.ID]
		if ps == nil {
			ps = []Participant{}
		}
		out[i] = Event{
			ID: r.ID, Title: r.Title, Description: r.Description,
			StartsAt: r.StartsAt, EndsAt: r.EndsAt, AllDay: r.AllDay,
			CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			Participants: ps,
		}
	}
	return out, nil
}

func newToken() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func validToken(t string) bool {
	b, err := base64.RawURLEncoding.DecodeString(t)
	return err == nil && len(b) == 32
}

func hashToken(t string) []byte {
	h := sha256.Sum256([]byte(t))
	return h[:]
}

// IsValidation reports whether err is a *ValidationError and returns it.
func IsValidation(err error) (*ValidationError, bool) {
	var ve *ValidationError
	ok := errors.As(err, &ve)
	return ve, ok
}
