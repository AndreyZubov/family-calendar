package httpapi

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AndreyZubov/family-calendar/backend/internal/auth"
	"github.com/AndreyZubov/family-calendar/backend/internal/store/db"
)

// UserQueries are the database queries the auth handlers need.
type UserQueries interface {
	UpsertUserByOIDC(ctx context.Context, arg db.UpsertUserByOIDCParams) (db.User, error)
	GetMembershipByUser(ctx context.Context, userID uuid.UUID) (db.GetMembershipByUserRow, error)
}

// AuthDeps are the dependencies of the sign-in flow.
type AuthDeps struct {
	Provider auth.IdentityProvider
	Sessions *auth.Sessions
	Flow     *auth.FlowSealer
	Cookies  auth.Cookies
	Users    UserQueries
}

// loginErrorPath is the frontend page that shows sign-in errors.
const loginErrorPath = "/login"

// handleLogin starts the Authorization Code Flow with PKCE:
// it generates state, nonce and code verifier, stores them in an encrypted
// short-lived cookie and redirects the browser to the identity provider.
func (a *API) handleLogin(w http.ResponseWriter, r *http.Request) {
	au := a.deps.Auth
	st := auth.NewFlowState(r.URL.Query().Get("return_to"), time.Now())

	sealed, err := au.Flow.Seal(st)
	if err != nil {
		logFrom(r.Context()).ErrorContext(r.Context(), "seal login flow", "err", err)
		writeError(w, r, http.StatusInternalServerError, "internal", "internal server error")
		return
	}
	http.SetCookie(w, au.Cookies.Flow(sealed))
	noStore(w)
	http.Redirect(w, r, au.Provider.AuthCodeURL(st.State, st.Nonce, st.Verifier), http.StatusFound)
}

// handleCallback finishes the flow: the provider redirects the browser here
// with ?code=...&state=... after the user signs in.
func (a *API) handleCallback(w http.ResponseWriter, r *http.Request) {
	au := a.deps.Auth
	ctx := r.Context()
	log := logFrom(ctx)
	noStore(w)

	// The flow cookie is single-use: delete it whatever happens next.
	http.SetCookie(w, au.Cookies.Clear(au.Cookies.FlowName()))

	fail := func(code string, attrs ...any) {
		log.WarnContext(ctx, "login failed", append([]any{"reason", code}, attrs...)...)
		http.Redirect(w, r, loginErrorPath+"?error="+url.QueryEscape(code), http.StatusFound)
	}

	ck, err := r.Cookie(au.Cookies.FlowName())
	if err != nil {
		fail("flow_expired") // no cookie: expired, or the callback was opened directly
		return
	}
	st, err := au.Flow.Open(ck.Value)
	if err != nil {
		fail("flow_expired")
		return
	}

	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		// e.g. access_denied when the user clicks "Cancel" at the provider.
		fail("provider_error", "provider_error", e)
		return
	}
	// state proves this callback belongs to a login this browser started
	// (login CSRF protection). Constant-time compare avoids timing leaks.
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(st.State)) != 1 {
		fail("state_mismatch")
		return
	}
	code := q.Get("code")
	if code == "" {
		fail("missing_code")
		return
	}

	id, err := au.Provider.Exchange(ctx, code, st.Verifier, st.Nonce)
	if errors.Is(err, auth.ErrEmailNotVerified) {
		fail("email_not_verified")
		return
	}
	if err != nil {
		fail("exchange_failed", "err", err)
		return
	}

	var avatar *string
	if id.Picture != "" {
		avatar = &id.Picture
	}
	user, err := au.Users.UpsertUserByOIDC(ctx, db.UpsertUserByOIDCParams{
		OidcIssuer: id.Issuer, OidcSubject: id.Subject,
		Email: id.Email, Name: id.Name, AvatarUrl: avatar,
	})
	if err != nil {
		fail("internal", "err", err)
		return
	}

	// Always issue a fresh session token on login (prevents session
	// fixation) and drop the previous session of this browser, if any.
	if old, err := r.Cookie(au.Cookies.SessionName()); err == nil {
		if err := au.Sessions.Destroy(ctx, old.Value); err != nil {
			log.WarnContext(ctx, "destroy previous session", "err", err)
		}
	}
	token, _, err := au.Sessions.Create(ctx, user.ID)
	if err != nil {
		fail("internal", "err", err)
		return
	}
	http.SetCookie(w, au.Cookies.Session(token, au.Sessions.TTL()))

	log.InfoContext(ctx, "user signed in", "user_id", user.ID)
	http.Redirect(w, r, st.ReturnTo, http.StatusFound)
}

// handleLogout deletes the session on the server and in the browser.
// POST only: a GET logout could be triggered by any <img> on another site.
func (a *API) handleLogout(w http.ResponseWriter, r *http.Request) {
	au := a.deps.Auth
	if ck, err := r.Cookie(au.Cookies.SessionName()); err == nil {
		if err := au.Sessions.Destroy(r.Context(), ck.Value); err != nil {
			logFrom(r.Context()).ErrorContext(r.Context(), "destroy session", "err", err)
			writeError(w, r, http.StatusInternalServerError, "internal", "internal server error")
			return
		}
	}
	http.SetCookie(w, au.Cookies.Clear(au.Cookies.SessionName()))
	w.WriteHeader(http.StatusNoContent)
}

type sessionCtxKey struct{}

// requireUser resolves the session cookie to a user and rejects the request
// with 401 if there is none. The session is available via sessionFrom.
func (a *API) requireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		au := a.deps.Auth
		noStore(w) // responses contain personal data: never cache

		ck, err := r.Cookie(au.Cookies.SessionName())
		if err != nil {
			writeError(w, r, http.StatusUnauthorized, "unauthenticated", "sign in required")
			return
		}
		sess, err := au.Sessions.Lookup(r.Context(), ck.Value)
		if errors.Is(err, auth.ErrNoSession) {
			// Expired or revoked: also remove the stale cookie.
			http.SetCookie(w, au.Cookies.Clear(au.Cookies.SessionName()))
			writeError(w, r, http.StatusUnauthorized, "unauthenticated", "sign in required")
			return
		}
		if err != nil {
			logFrom(r.Context()).ErrorContext(r.Context(), "session lookup", "err", err)
			writeError(w, r, http.StatusInternalServerError, "internal", "internal server error")
			return
		}
		if sess.Renewed {
			http.SetCookie(w, au.Cookies.Session(ck.Value, au.Sessions.TTL()))
		}

		// Every log line of this request, including the access log, now
		// carries the user ID.
		setRequestUser(r.Context(), sess.User.ID.String())
		log := logFrom(r.Context()).With(slog.String("user_id", sess.User.ID.String()))
		ctx := context.WithValue(r.Context(), loggerKey, log)
		ctx = context.WithValue(ctx, sessionCtxKey{}, sess)
		next(w, r.WithContext(ctx))
	}
}

// sessionFrom returns the session set by requireUser.
func sessionFrom(ctx context.Context) auth.Session {
	s, _ := ctx.Value(sessionCtxKey{}).(auth.Session)
	return s
}

type meResponse struct {
	User   userJSON    `json:"user"`
	Family *familyJSON `json:"family"` // null until the user creates or joins one
}

type userJSON struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	AvatarURL *string   `json:"avatarUrl"`
}

type familyJSON struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Role string    `json:"role"`
}

// handleMe returns the signed-in user; the frontend calls it on startup to
// decide between the login page and the calendar.
func (a *API) handleMe(w http.ResponseWriter, r *http.Request) {
	u := sessionFrom(r.Context()).User
	resp := meResponse{User: userJSON{ID: u.ID, Email: u.Email, Name: u.Name, AvatarURL: u.AvatarUrl}}

	m, err := a.deps.Auth.Users.GetMembershipByUser(r.Context(), u.ID)
	switch {
	case err == nil:
		resp.Family = &familyJSON{ID: m.FamilyID, Name: m.FamilyName, Role: m.Role}
	case errors.Is(err, pgx.ErrNoRows):
		// No family yet.
	default:
		logFrom(r.Context()).ErrorContext(r.Context(), "get membership", "err", err)
		writeError(w, r, http.StatusInternalServerError, "internal", "internal server error")
		return
	}
	writeJSON(w, r, http.StatusOK, resp)
}

func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}
