// Package calendar holds the business rules of the family calendar:
// families, invites, events and participants. It knows nothing about HTTP;
// handlers translate requests into calls here and errors back into status
// codes.
//
// Every method takes the acting Member, and all data access is scoped to
// that member's family, so one family can never see or change another's data.
package calendar

import (
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Roles within a family.
const (
	RoleOwner  = "owner"
	RoleMember = "member"
)

// Member is the signed-in user in the context of their family.
type Member struct {
	UserID     uuid.UUID
	FamilyID   uuid.UUID
	FamilyName string
	Role       string
}

// IsOwner reports whether the member created (owns) the family.
func (m Member) IsOwner() bool { return m.Role == RoleOwner }

// FamilyMember is a person in the family list.
type FamilyMember struct {
	UserID    uuid.UUID
	Email     string
	Name      string
	AvatarURL *string
	Role      string
	JoinedAt  time.Time
}

// Participant is a family member attending an event.
type Participant struct {
	UserID    uuid.UUID
	Name      string
	AvatarURL *string
}

// Event is a calendar entry with its participants.
type Event struct {
	ID           uuid.UUID
	Title        string
	Description  string
	StartsAt     time.Time
	EndsAt       time.Time
	AllDay       bool
	CreatedBy    *uuid.UUID
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Participants []Participant
}

// EventInput is what a client may set on an event.
type EventInput struct {
	Title       string
	Description string
	StartsAt    time.Time
	EndsAt      time.Time
	AllDay      bool
	// ParticipantIDs is the full participant list. On update, nil keeps the
	// current participants; an empty slice removes all of them.
	ParticipantIDs []uuid.UUID
}

// Invite is a freshly created invitation. Token is shown once and never
// stored: the database keeps only its hash.
type Invite struct {
	Token     string
	ExpiresAt time.Time
}

// Limits mirror the CHECK constraints in the schema, so users get a clear
// validation error instead of a database error.
const (
	MaxTitleLen       = 200
	MaxDescriptionLen = 5000
	MaxFamilyNameLen  = 100
	MaxParticipants   = 50
	MaxEventDuration  = 366 * 24 * time.Hour
	MaxListRange      = 400 * 24 * time.Hour // a year view plus margins
	InviteTTL         = 7 * 24 * time.Hour
)

// Errors returned by the service. Handlers map them to HTTP status codes.
var (
	ErrNotFound         = errors.New("not found")
	ErrNoFamily         = errors.New("user has no family yet")
	ErrAlreadyInFamily  = errors.New("user already belongs to a family")
	ErrInviteInvalid    = errors.New("invite is invalid, expired or already used")
	ErrForbidden        = errors.New("not allowed")
	ErrOwnerCannotLeave = errors.New("the family owner cannot leave the family")
)

// ValidationError lists invalid input fields with a reason for each.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + ": " + e.Fields[k]
	}
	return "invalid input: " + strings.Join(parts, "; ")
}

// validator collects field errors.
type validator map[string]string

func (v validator) add(field, msg string) {
	if _, ok := v[field]; !ok {
		v[field] = msg
	}
}

func (v validator) err() error {
	if len(v) == 0 {
		return nil
	}
	return &ValidationError{Fields: v}
}

// normalize validates and cleans an event input in place.
func (in *EventInput) normalize() error {
	v := validator{}

	in.Title = strings.TrimSpace(in.Title)
	switch n := utf8.RuneCountInString(in.Title); {
	case n == 0:
		v.add("title", "required")
	case n > MaxTitleLen:
		v.add("title", "must be at most 200 characters")
	}
	in.Description = strings.TrimSpace(in.Description)
	if utf8.RuneCountInString(in.Description) > MaxDescriptionLen {
		v.add("description", "must be at most 5000 characters")
	}

	switch {
	case in.StartsAt.IsZero():
		v.add("startsAt", "required")
	case in.EndsAt.IsZero():
		v.add("endsAt", "required")
	case in.EndsAt.Before(in.StartsAt):
		v.add("endsAt", "must not be before startsAt")
	case in.EndsAt.Sub(in.StartsAt) > MaxEventDuration:
		v.add("endsAt", "event must not be longer than 366 days")
	}
	in.StartsAt, in.EndsAt = in.StartsAt.UTC(), in.EndsAt.UTC()

	if in.ParticipantIDs != nil {
		in.ParticipantIDs = dedupe(in.ParticipantIDs)
		if len(in.ParticipantIDs) > MaxParticipants {
			v.add("participantIds", "too many participants")
		}
	}
	return v.err()
}

func validateFamilyName(name string) (string, error) {
	name = strings.TrimSpace(name)
	v := validator{}
	switch n := utf8.RuneCountInString(name); {
	case n == 0:
		v.add("name", "required")
	case n > MaxFamilyNameLen:
		v.add("name", "must be at most 100 characters")
	}
	return name, v.err()
}

func validateRange(from, to time.Time) error {
	v := validator{}
	switch {
	case from.IsZero():
		v.add("from", "required")
	case to.IsZero():
		v.add("to", "required")
	case !to.After(from):
		v.add("to", "must be after from")
	case to.Sub(from) > MaxListRange:
		v.add("to", "range must not exceed 400 days")
	}
	return v.err()
}

func dedupe(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
