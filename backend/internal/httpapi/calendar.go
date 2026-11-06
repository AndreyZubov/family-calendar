package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/AndreyZubov/family-calendar/backend/internal/calendar"
)

// ---------------------------------------------------------------------------
// JSON shapes (see backend/api/openapi.yaml)

type eventJSON struct {
	ID           uuid.UUID         `json:"id"`
	Title        string            `json:"title"`
	Description  string            `json:"description"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	AllDay       bool              `json:"allDay"`
	CreatedBy    *uuid.UUID        `json:"createdBy"`
	CreatedAt    time.Time         `json:"createdAt"`
	UpdatedAt    time.Time         `json:"updatedAt"`
	Participants []participantJSON `json:"participants"`
}

type participantJSON struct {
	UserID    uuid.UUID `json:"userId"`
	Name      string    `json:"name"`
	AvatarURL *string   `json:"avatarUrl"`
}

type eventInputJSON struct {
	Title          string      `json:"title"`
	Description    string      `json:"description"`
	StartsAt       time.Time   `json:"startsAt"`
	EndsAt         time.Time   `json:"endsAt"`
	AllDay         bool        `json:"allDay"`
	ParticipantIDs []uuid.UUID `json:"participantIds"`
}

type memberJSON struct {
	UserID    uuid.UUID `json:"userId"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	AvatarURL *string   `json:"avatarUrl"`
	Role      string    `json:"role"`
	JoinedAt  time.Time `json:"joinedAt"`
}

type listJSON[T any] struct {
	Items []T `json:"items"`
}

func toEventJSON(e calendar.Event) eventJSON {
	ps := make([]participantJSON, len(e.Participants))
	for i, p := range e.Participants {
		ps[i] = participantJSON{UserID: p.UserID, Name: p.Name, AvatarURL: p.AvatarURL}
	}
	// All timestamps leave the API in UTC: pgx returns them in the server's
	// local zone, which must not leak into the contract.
	return eventJSON{
		ID: e.ID, Title: e.Title, Description: e.Description,
		StartsAt: e.StartsAt.UTC(), EndsAt: e.EndsAt.UTC(), AllDay: e.AllDay,
		CreatedBy: e.CreatedBy, CreatedAt: e.CreatedAt.UTC(), UpdatedAt: e.UpdatedAt.UTC(),
		Participants: ps,
	}
}

func (in eventInputJSON) toInput() calendar.EventInput {
	return calendar.EventInput{
		Title: in.Title, Description: in.Description,
		StartsAt: in.StartsAt, EndsAt: in.EndsAt, AllDay: in.AllDay,
		ParticipantIDs: in.ParticipantIDs,
	}
}

func familyJSONFrom(m calendar.Member) *familyJSON {
	return &familyJSON{ID: m.FamilyID, Name: m.FamilyName, Role: m.Role}
}

// ---------------------------------------------------------------------------
// Middleware and helpers

type memberCtxKey struct{}

// requireFamily resolves the signed-in user's family (403 no_family if they
// have none yet). Must run inside requireUser.
func (a *API) requireFamily(next http.HandlerFunc) http.HandlerFunc {
	return a.requireUser(func(w http.ResponseWriter, r *http.Request) {
		m, err := a.deps.Calendar.Membership(r.Context(), sessionFrom(r.Context()).User.ID)
		if err != nil {
			a.writeServiceError(w, r, err)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), memberCtxKey{}, m)))
	})
}

func memberFrom(ctx context.Context) calendar.Member {
	m, _ := ctx.Value(memberCtxKey{}).(calendar.Member)
	return m
}

// pathUUID parses a {name} path parameter; on failure it writes 404 (a
// malformed ID cannot exist) and returns false.
func pathUUID(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		writeError(w, r, http.StatusNotFound, "not_found", "resource not found")
		return uuid.Nil, false
	}
	return id, true
}

// writeServiceError maps calendar errors to HTTP responses. Unknown errors
// are logged and become a generic 500 without details.
func (a *API) writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	if ve, ok := calendar.IsValidation(err); ok {
		writeJSON(w, r, http.StatusUnprocessableEntity, errorBody{Error: errorDetail{
			Code: "validation_failed", Message: "some fields are invalid", Fields: ve.Fields,
		}})
		return
	}
	switch {
	case errors.Is(err, calendar.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, calendar.ErrNoFamily):
		writeError(w, r, http.StatusForbidden, "no_family", "create or join a family first")
	case errors.Is(err, calendar.ErrAlreadyInFamily):
		writeError(w, r, http.StatusConflict, "already_in_family", "you already belong to a family")
	case errors.Is(err, calendar.ErrInviteInvalid):
		writeError(w, r, http.StatusNotFound, "invite_invalid", "invite is invalid, expired or already used")
	case errors.Is(err, calendar.ErrOwnerCannotLeave):
		writeError(w, r, http.StatusConflict, "owner_cannot_leave", "the family owner cannot leave the family")
	case errors.Is(err, calendar.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "forbidden", "only the family owner can do this")
	case errors.Is(err, context.Canceled):
		// Client went away; nobody will read the response.
		logFrom(r.Context()).InfoContext(r.Context(), "request cancelled by client")
	default:
		logFrom(r.Context()).ErrorContext(r.Context(), "request failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, "internal", "internal server error")
	}
}

// ---------------------------------------------------------------------------
// Family

// POST /api/family {"name": "..."}
func (a *API) handleCreateFamily(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	m, err := a.deps.Calendar.CreateFamily(r.Context(), sessionFrom(r.Context()).User.ID, req.Name)
	if err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusCreated, familyJSONFrom(m))
}

// GET /api/family/members
func (a *API) handleListMembers(w http.ResponseWriter, r *http.Request) {
	ms, err := a.deps.Calendar.ListMembers(r.Context(), memberFrom(r.Context()))
	if err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	items := make([]memberJSON, len(ms))
	for i, m := range ms {
		items[i] = memberJSON{UserID: m.UserID, Email: m.Email, Name: m.Name, AvatarURL: m.AvatarURL, Role: m.Role, JoinedAt: m.JoinedAt.UTC()}
	}
	writeJSON(w, r, http.StatusOK, listJSON[memberJSON]{Items: items})
}

// DELETE /api/family/members/{userId}
func (a *API) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	uid, ok := pathUUID(w, r, "userId")
	if !ok {
		return
	}
	if err := a.deps.Calendar.RemoveMember(r.Context(), memberFrom(r.Context()), uid); err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/family/invites
//
// The token is returned once, inside a link with a URL fragment (#...):
// browsers never send fragments to servers, so the token does not end up in
// access logs or Referer headers when the link is opened.
func (a *API) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	inv, err := a.deps.Calendar.CreateInvite(r.Context(), memberFrom(r.Context()))
	if err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	base := ""
	if a.deps.BaseURL != nil {
		base = a.deps.BaseURL.String()
	}
	writeJSON(w, r, http.StatusCreated, struct {
		Token     string    `json:"token"`
		URL       string    `json:"url"`
		ExpiresAt time.Time `json:"expiresAt"`
	}{inv.Token, base + "/invite#" + inv.Token, inv.ExpiresAt.UTC()})
}

// POST /api/invites/accept {"token": "..."}
func (a *API) handleAcceptInvite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	m, err := a.deps.Calendar.AcceptInvite(r.Context(), sessionFrom(r.Context()).User.ID, req.Token)
	if err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	logFrom(r.Context()).InfoContext(r.Context(), "joined family", "family_id", m.FamilyID)
	writeJSON(w, r, http.StatusOK, familyJSONFrom(m))
}

// ---------------------------------------------------------------------------
// Events

// GET /api/events?from=<RFC3339>&to=<RFC3339>
func (a *API) handleListEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, errFrom := parseQueryTime(q.Get("from"))
	to, errTo := parseQueryTime(q.Get("to"))
	if errFrom != nil || errTo != nil {
		fields := map[string]string{}
		if errFrom != nil {
			fields["from"] = errFrom.Error()
		}
		if errTo != nil {
			fields["to"] = errTo.Error()
		}
		a.writeServiceError(w, r, &calendar.ValidationError{Fields: fields})
		return
	}
	evs, err := a.deps.Calendar.ListEvents(r.Context(), memberFrom(r.Context()), from, to)
	if err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	items := make([]eventJSON, len(evs))
	for i, e := range evs {
		items[i] = toEventJSON(e)
	}
	writeJSON(w, r, http.StatusOK, listJSON[eventJSON]{Items: items})
}

func parseQueryTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, errors.New("required")
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, errors.New("must be an RFC 3339 timestamp, e.g. 2026-11-01T00:00:00Z")
	}
	return t, nil
}

// POST /api/events
func (a *API) handleCreateEvent(w http.ResponseWriter, r *http.Request) {
	var req eventInputJSON
	if !decodeJSON(w, r, &req) {
		return
	}
	ev, err := a.deps.Calendar.CreateEvent(r.Context(), memberFrom(r.Context()), req.toInput())
	if err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/events/"+ev.ID.String())
	writeJSON(w, r, http.StatusCreated, toEventJSON(ev))
}

// GET /api/events/{id}
func (a *API) handleGetEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	ev, err := a.deps.Calendar.GetEvent(r.Context(), memberFrom(r.Context()), id)
	if err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, toEventJSON(ev))
}

// PUT /api/events/{id}
func (a *API) handleUpdateEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req eventInputJSON
	if !decodeJSON(w, r, &req) {
		return
	}
	ev, err := a.deps.Calendar.UpdateEvent(r.Context(), memberFrom(r.Context()), id, req.toInput())
	if err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, toEventJSON(ev))
}

// DELETE /api/events/{id}
func (a *API) handleDeleteEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if err := a.deps.Calendar.DeleteEvent(r.Context(), memberFrom(r.Context()), id); err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/events/{id}/participants {"userId": "..."}
func (a *API) handleAddParticipant(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		UserID uuid.UUID `json:"userId"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := a.deps.Calendar.AddParticipant(r.Context(), memberFrom(r.Context()), id, req.UserID); err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /api/events/{id}/participants/{userId}
func (a *API) handleRemoveParticipant(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	uid, ok := pathUUID(w, r, "userId")
	if !ok {
		return
	}
	if err := a.deps.Calendar.RemoveParticipant(r.Context(), memberFrom(r.Context()), id, uid); err != nil {
		a.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
