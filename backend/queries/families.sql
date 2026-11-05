-- name: CreateFamily :one
INSERT INTO families (name)
VALUES (@name)
RETURNING *;

-- name: AddFamilyMember :exec
INSERT INTO family_members (family_id, user_id, role)
VALUES (@family_id, @user_id, @role);

-- name: GetMembershipByUser :one
-- The family the user belongs to (MVP: at most one).
SELECT fm.family_id, fm.role, fm.joined_at, f.name AS family_name
FROM family_members fm
JOIN families f ON f.id = fm.family_id
WHERE fm.user_id = @user_id;

-- name: ListFamilyMembers :many
SELECT u.id, u.email, u.name, u.avatar_url, fm.role, fm.joined_at
FROM family_members fm
JOIN users u ON u.id = fm.user_id
WHERE fm.family_id = @family_id
ORDER BY fm.joined_at, u.name;

-- name: RemoveFamilyMember :execrows
-- Event participations of this member are removed by ON DELETE CASCADE.
DELETE FROM family_members
WHERE family_id = @family_id
  AND user_id = @user_id;

-- name: CreateInvite :one
INSERT INTO family_invites (family_id, token_hash, created_by, expires_at)
VALUES (@family_id, @token_hash, @created_by, @expires_at)
RETURNING id, family_id, created_by, expires_at, created_at;

-- name: ConsumeInvite :one
-- Single-use: marks a valid invite as used and returns its family in one
-- atomic statement, so the same link cannot be accepted twice concurrently.
UPDATE family_invites
SET used_at = now(),
    used_by = @used_by
WHERE token_hash = @token_hash
  AND used_at IS NULL
  AND expires_at > now()
RETURNING family_id;
