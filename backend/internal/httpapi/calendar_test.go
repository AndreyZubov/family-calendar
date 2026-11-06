package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AndreyZubov/family-calendar/backend/internal/auth"
	"github.com/AndreyZubov/family-calendar/backend/internal/calendar"
	"github.com/AndreyZubov/family-calendar/backend/internal/store"
	"github.com/AndreyZubov/family-calendar/backend/internal/store/db"
	"github.com/AndreyZubov/family-calendar/backend/internal/testdb"
)

// apiEnv runs the full HTTP stack against its own fresh database. Users get
// a session directly (the sign-in flow itself is covered in auth_test.go).
type apiEnv struct {
	t        *testing.T
	ctx      context.Context
	api      *API
	st       *store.Store
	sessions *auth.Sessions
	ip       atomic.Int32
}

type apiUser struct {
	id     uuid.UUID
	cookie *http.Cookie
}

func newAPIEnv(t *testing.T) *apiEnv {
	t.Helper()
	t.Parallel()
	ctx := context.Background()
	st, err := store.New(ctx, store.Config{URL: testdb.New(t), MaxConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	flow, _ := auth.NewFlowSealer(bytes.Repeat([]byte{1}, 32))
	sessions := auth.NewSessions(st, time.Hour)
	base, _ := url.Parse("https://calendar.example.com")
	api, err := New(Deps{
		Log: slog.New(slog.DiscardHandler), DB: st, Version: "test",
		Auth: AuthDeps{
			Provider: &fakeProvider{}, Sessions: sessions, Flow: flow,
			Cookies: auth.Cookies{Secure: true}, Users: st,
		},
		Calendar: calendar.NewService(st),
		BaseURL:  base,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &apiEnv{t: t, ctx: ctx, api: api, st: st, sessions: sessions}
}

func (e *apiEnv) user(name string) apiUser {
	e.t.Helper()
	u, err := e.st.UpsertUserByOIDC(e.ctx, db.UpsertUserByOIDCParams{
		OidcIssuer: "https://issuer.test", OidcSubject: uuid.NewString(), Email: name + "@example.com", Name: name,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	token, _, err := e.sessions.Create(e.ctx, u.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return apiUser{id: u.ID, cookie: &http.Cookie{Name: "__Host-session", Value: token}}
}

type resp struct {
	code   int
	header http.Header
	body   []byte
}

func (r resp) decode(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
}

func (r resp) errCode(t *testing.T) string {
	var b errorBody
	r.decode(t, &b)
	return b.Error.Code
}

// do sends a request as u (zero user = anonymous). A distinct client IP per
// request keeps the per-IP rate limiter out of the way.
func (e *apiEnv) do(u apiUser, method, path string, body any) resp {
	e.t.Helper()
	var rd io.Reader
	if s, ok := body.(string); ok {
		rd = strings.NewReader(s)
	} else if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	n := e.ip.Add(1)
	req.RemoteAddr = fmt.Sprintf("10.%d.%d.%d:1234", n>>16&255, n>>8&255, n&255)
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if u.cookie != nil {
		req.AddCookie(u.cookie)
	}
	rec := httptest.NewRecorder()
	e.api.ServeHTTP(rec, req)
	return resp{code: rec.Code, header: rec.Header(), body: rec.Body.Bytes()}
}

func (e *apiEnv) expect(r resp, code int, errCode string) {
	e.t.Helper()
	if r.code != code {
		e.t.Fatalf("status %d, want %d; body: %s", r.code, code, r.body)
	}
	if errCode != "" {
		if got := r.errCode(e.t); got != errCode {
			e.t.Fatalf("error code %q, want %q", got, errCode)
		}
	}
}

func TestCalendarAPIFlow(t *testing.T) {
	e := newAPIEnv(t)
	alice, bob := e.user("alice"), e.user("bob")

	// Anonymous and family-less users.
	e.expect(e.do(apiUser{}, "GET", "/api/events?from=2026-11-01T00:00:00Z&to=2026-12-01T00:00:00Z", nil), 401, "unauthenticated")
	e.expect(e.do(alice, "GET", "/api/events?from=2026-11-01T00:00:00Z&to=2026-12-01T00:00:00Z", nil), 403, "no_family")

	// Create a family.
	r := e.do(alice, "POST", "/api/family", map[string]string{"name": "Ivanovs"})
	e.expect(r, 201, "")
	var fam familyJSON
	r.decode(t, &fam)
	if fam.Name != "Ivanovs" || fam.Role != "owner" {
		t.Errorf("family = %+v", fam)
	}
	e.expect(e.do(alice, "POST", "/api/family", map[string]string{"name": "Again"}), 409, "already_in_family")

	// /api/me now includes the family.
	var me meResponse
	e.do(alice, "GET", "/api/me", nil).decode(t, &me)
	if me.Family == nil || me.Family.ID != fam.ID {
		t.Errorf("me.family = %+v", me.Family)
	}

	// Invite Bob.
	r = e.do(alice, "POST", "/api/family/invites", nil)
	e.expect(r, 201, "")
	var inv struct{ Token, URL string }
	r.decode(t, &inv)
	if !strings.HasPrefix(inv.URL, "https://calendar.example.com/invite#") || !strings.HasSuffix(inv.URL, inv.Token) {
		t.Errorf("invite url = %q", inv.URL)
	}
	e.expect(e.do(bob, "POST", "/api/invites/accept", map[string]string{"token": inv.Token}), 200, "")
	e.expect(e.do(bob, "POST", "/api/invites/accept", map[string]string{"token": inv.Token}), 409, "already_in_family")

	var members listJSON[memberJSON]
	e.do(bob, "GET", "/api/family/members", nil).decode(t, &members)
	if len(members.Items) != 2 {
		t.Fatalf("members = %+v", members.Items)
	}

	// Create an event with both as participants.
	r = e.do(bob, "POST", "/api/events", map[string]any{
		"title": "Dinner", "startsAt": "2026-11-10T18:00:00+03:00", "endsAt": "2026-11-10T20:00:00+03:00",
		"participantIds": []uuid.UUID{alice.id, bob.id},
	})
	e.expect(r, 201, "")
	var ev eventJSON
	r.decode(t, &ev)
	if r.header.Get("Location") != "/api/events/"+ev.ID.String() {
		t.Errorf("Location = %q", r.header.Get("Location"))
	}
	if !ev.StartsAt.Equal(time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC)) || len(ev.Participants) != 2 || *ev.CreatedBy != bob.id {
		t.Errorf("event = %+v", ev)
	}
	for _, field := range []string{"startsAt", "endsAt", "createdAt", "updatedAt"} {
		if !regexp.MustCompile(`"` + field + `":"[^"]+Z"`).Match(r.body) {
			t.Errorf("%s must be returned in UTC: %s", field, r.body)
		}
	}

	// List, get.
	var list listJSON[eventJSON]
	e.do(alice, "GET", "/api/events?from=2026-11-01T00:00:00Z&to=2026-12-01T00:00:00Z", nil).decode(t, &list)
	if len(list.Items) != 1 || list.Items[0].ID != ev.ID {
		t.Fatalf("list = %+v", list.Items)
	}
	e.expect(e.do(alice, "GET", "/api/events/"+ev.ID.String(), nil), 200, "")

	// Update (participants omitted → kept).
	r = e.do(alice, "PUT", "/api/events/"+ev.ID.String(), map[string]any{
		"title": "Late dinner", "startsAt": "2026-11-10T19:00:00Z", "endsAt": "2026-11-10T21:00:00Z",
	})
	e.expect(r, 200, "")
	r.decode(t, &ev)
	if ev.Title != "Late dinner" || len(ev.Participants) != 2 {
		t.Errorf("updated = %+v", ev)
	}

	// Participants endpoints.
	path := "/api/events/" + ev.ID.String() + "/participants"
	e.expect(e.do(alice, "DELETE", path+"/"+bob.id.String(), nil), 204, "")
	e.expect(e.do(alice, "DELETE", path+"/"+bob.id.String(), nil), 404, "not_found")
	e.expect(e.do(alice, "POST", path, map[string]any{"userId": bob.id}), 204, "")
	e.expect(e.do(alice, "POST", path, map[string]any{"userId": bob.id}), 204, "") // idempotent

	// Delete.
	e.expect(e.do(bob, "DELETE", "/api/events/"+ev.ID.String(), nil), 204, "")
	e.expect(e.do(bob, "GET", "/api/events/"+ev.ID.String(), nil), 404, "not_found")
}

func TestCalendarAPIIsolationAndPermissions(t *testing.T) {
	e := newAPIEnv(t)
	alice, bob, mallory := e.user("alice"), e.user("bob"), e.user("mallory")
	e.expect(e.do(alice, "POST", "/api/family", map[string]string{"name": "Ivanovs"}), 201, "")
	e.expect(e.do(mallory, "POST", "/api/family", map[string]string{"name": "Evil"}), 201, "")

	var inv struct{ Token string }
	e.do(alice, "POST", "/api/family/invites", nil).decode(t, &inv)
	e.expect(e.do(bob, "POST", "/api/invites/accept", map[string]string{"token": inv.Token}), 200, "")

	var ev eventJSON
	e.do(alice, "POST", "/api/events", map[string]any{
		"title": "Private", "startsAt": "2026-11-10T18:00:00Z", "endsAt": "2026-11-10T19:00:00Z",
	}).decode(t, &ev)
	id := ev.ID.String()

	// Another family sees nothing and cannot change anything (IDOR).
	e.expect(e.do(mallory, "GET", "/api/events/"+id, nil), 404, "not_found")
	e.expect(e.do(mallory, "PUT", "/api/events/"+id, map[string]any{"title": "x", "startsAt": "2026-11-10T18:00:00Z", "endsAt": "2026-11-10T18:00:00Z"}), 404, "not_found")
	e.expect(e.do(mallory, "DELETE", "/api/events/"+id, nil), 404, "not_found")
	e.expect(e.do(mallory, "POST", "/api/events/"+id+"/participants", map[string]any{"userId": mallory.id}), 404, "not_found")
	var list listJSON[eventJSON]
	e.do(mallory, "GET", "/api/events?from=2026-11-01T00:00:00Z&to=2026-12-01T00:00:00Z", nil).decode(t, &list)
	if len(list.Items) != 0 {
		t.Errorf("mallory sees %d events", len(list.Items))
	}

	// A stranger cannot be added as a participant.
	r := e.do(alice, "POST", "/api/events/"+id+"/participants", map[string]any{"userId": mallory.id})
	e.expect(r, 422, "validation_failed")

	// Member permissions.
	e.expect(e.do(bob, "DELETE", "/api/family/members/"+alice.id.String(), nil), 403, "forbidden")
	e.expect(e.do(alice, "DELETE", "/api/family/members/"+alice.id.String(), nil), 409, "owner_cannot_leave")
	e.expect(e.do(alice, "DELETE", "/api/family/members/"+mallory.id.String(), nil), 404, "not_found")
	e.expect(e.do(bob, "DELETE", "/api/family/members/"+bob.id.String(), nil), 204, "") // leave
	e.expect(e.do(bob, "GET", "/api/family/members", nil), 403, "no_family")
}

func TestCalendarAPIInputErrors(t *testing.T) {
	e := newAPIEnv(t)
	alice := e.user("alice")
	e.expect(e.do(alice, "POST", "/api/family", map[string]string{"name": "Ivanovs"}), 201, "")

	valid := `{"title":"x","startsAt":"2026-11-10T18:00:00Z","endsAt":"2026-11-10T19:00:00Z"}`

	// Validation: all problems reported at once, by field.
	r := e.do(alice, "POST", "/api/events", `{"title":"  ","startsAt":"2026-11-10T18:00:00Z","endsAt":"2026-11-10T17:00:00Z"}`)
	e.expect(r, 422, "validation_failed")
	var b errorBody
	r.decode(t, &b)
	if b.Error.Fields["title"] == "" || b.Error.Fields["endsAt"] == "" {
		t.Errorf("fields = %v", b.Error.Fields)
	}

	cases := []struct {
		name, method, path, body, contentType string
		code                                  int
		errCode                               string
	}{
		{"malformed JSON", "POST", "/api/events", `{"title":`, "", 400, "invalid_json"},
		{"unknown field", "POST", "/api/events", `{"title":"x","color":"red"}`, "", 400, "invalid_json"},
		{"wrong type", "POST", "/api/events", `{"title":42}`, "", 400, "invalid_json"},
		{"bad timestamp", "POST", "/api/events", `{"title":"x","startsAt":"tomorrow"}`, "", 400, "invalid_json"},
		{"two objects", "POST", "/api/events", valid + valid, "", 400, "invalid_json"},
		{"form instead of JSON", "POST", "/api/events", "title=x", "application/x-www-form-urlencoded", 415, "unsupported_media_type"},
		{"text/plain (CSRF-able type)", "POST", "/api/events", valid, "text/plain", 415, "unsupported_media_type"},
		{"bad event id", "GET", "/api/events/not-a-uuid", "", "", 404, "not_found"},
		{"unknown event", "GET", "/api/events/" + uuid.NewString(), "", "", 404, "not_found"},
		{"list without range", "GET", "/api/events", "", "", 422, "validation_failed"},
		{"list bad range", "GET", "/api/events?from=yesterday&to=2026-12-01T00:00:00Z", "", "", 422, "validation_failed"},
		{"list range too wide", "GET", "/api/events?from=2026-01-01T00:00:00Z&to=2028-01-01T00:00:00Z", "", "", 422, "validation_failed"},
		{"bad invite token", "POST", "/api/invites/accept", `{"token":"nope"}`, "", 404, "invite_invalid"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var body any
			if c.body != "" {
				body = c.body
			}
			req := httptest.NewRequest(c.method, c.path, nil)
			if body != nil {
				req = httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
				ct := c.contentType
				if ct == "" {
					ct = "application/json"
				}
				req.Header.Set("Content-Type", ct)
			}
			req.AddCookie(alice.cookie)
			req.RemoteAddr = "10.200.0.1:1"
			rec := httptest.NewRecorder()
			e.api.ServeHTTP(rec, req)
			r := resp{code: rec.Code, body: rec.Body.Bytes()}
			if r.code != c.code || r.errCode(t) != c.errCode {
				t.Errorf("got %d %q, want %d %q; body %s", r.code, r.errCode(t), c.code, c.errCode, r.body)
			}
		})
	}

	// Invalid invite for a user without a family.
	bob := e.user("bob")
	e.expect(e.do(bob, "POST", "/api/invites/accept", map[string]string{"token": "nope"}), 404, "invite_invalid")
}
