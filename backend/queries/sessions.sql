-- name: CreateSession :exec
INSERT INTO sessions (token_hash, user_id, expires_at)
VALUES (@token_hash, @user_id, @expires_at);

-- name: GetSessionUser :one
-- Resolves a session cookie to its user. Expired sessions are not returned.
SELECT sqlc.embed(users), s.expires_at AS session_expires_at, s.last_seen_at AS session_last_seen_at
FROM sessions s
JOIN users ON users.id = s.user_id
WHERE s.token_hash = @token_hash
  AND s.expires_at > now();

-- name: TouchSession :exec
-- Sliding expiration: extends an active session.
UPDATE sessions
SET last_seen_at = now(),
    expires_at   = @expires_at
WHERE token_hash = @token_hash;

-- name: DeleteSession :exec
DELETE FROM sessions
WHERE token_hash = @token_hash;

-- name: DeleteUserSessions :exec
-- "Sign out everywhere".
DELETE FROM sessions
WHERE user_id = @user_id;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions
WHERE expires_at <= now();
