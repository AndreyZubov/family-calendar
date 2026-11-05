-- name: UpsertUserByOIDC :one
-- Called on every successful login: creates the user on first sign-in and
-- refreshes profile data from the identity provider afterwards.
INSERT INTO users (oidc_issuer, oidc_subject, email, name, avatar_url)
VALUES (@oidc_issuer, @oidc_subject, @email, @name, sqlc.narg('avatar_url'))
ON CONFLICT (oidc_issuer, oidc_subject) DO UPDATE
SET email      = EXCLUDED.email,
    name       = EXCLUDED.name,
    avatar_url = EXCLUDED.avatar_url
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users
WHERE id = @id;
