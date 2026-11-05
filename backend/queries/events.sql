-- name: CreateEvent :one
INSERT INTO events (family_id, title, description, starts_at, ends_at, all_day, created_by)
VALUES (@family_id, @title, @description, @starts_at, @ends_at, @all_day, @created_by)
RETURNING *;

-- name: GetEvent :one
SELECT * FROM events
WHERE id = @id
  AND family_id = @family_id;

-- name: ListEventsInRange :many
-- Events overlapping the half-open window [from, to).
SELECT * FROM events
WHERE family_id = @family_id
  AND starts_at < @range_to
  AND ends_at > @range_from
ORDER BY starts_at, id;

-- name: UpdateEvent :one
UPDATE events
SET title       = @title,
    description = @description,
    starts_at   = @starts_at,
    ends_at     = @ends_at,
    all_day     = @all_day
WHERE id = @id
  AND family_id = @family_id
RETURNING *;

-- name: DeleteEvent :execrows
DELETE FROM events
WHERE id = @id
  AND family_id = @family_id;

-- name: AddEventParticipant :execrows
-- Composite foreign keys reject users who are not members of the event's
-- family. Adding an existing participant is a no-op (0 rows).
INSERT INTO event_participants (event_id, family_id, user_id)
VALUES (@event_id, @family_id, @user_id)
ON CONFLICT (event_id, user_id) DO NOTHING;

-- name: RemoveEventParticipant :execrows
DELETE FROM event_participants
WHERE event_id = @event_id
  AND family_id = @family_id
  AND user_id = @user_id;

-- name: ListParticipantsForEvents :many
-- Participants of several events in one query (avoids N+1 in list views).
SELECT ep.event_id, u.id AS user_id, u.name, u.avatar_url
FROM event_participants ep
JOIN users u ON u.id = ep.user_id
WHERE ep.family_id = @family_id
  AND ep.event_id = ANY (@event_ids::uuid[])
ORDER BY ep.event_id, ep.added_at;
