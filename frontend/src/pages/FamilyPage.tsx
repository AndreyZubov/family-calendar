import { useState } from 'react'
import { useNavigate } from 'react-router'

import { errorMessage } from '../api/errors'
import { useCreateInvite, useMembers, useRemoveMember } from '../api/queries'
import { Avatar } from '../components/Avatar'
import { useCurrentUser } from '../components/useCurrentUser'
import { ErrorBox, Loading } from '../components/Status'
import { formatShortDate } from '../lib/dates'

export function FamilyPage() {
  const { user, family } = useCurrentUser()
  const members = useMembers()
  const remove = useRemoveMember()
  const navigate = useNavigate()
  const isOwner = family?.role === 'owner'

  function removeMember(userId: string, name: string) {
    const self = userId === user.id
    const question = self
      ? 'Leave this family? You will lose access to its calendar.'
      : `Remove ${name} from the family? They will also be removed from all events.`
    if (!window.confirm(question)) return
    remove.mutate(userId, {
      onSuccess: () => {
        if (self) void navigate('/')
      },
    })
  }

  return (
    <div className="stack">
      <section className="card">
        <h1>{family?.name}</h1>
        {members.isPending && <Loading />}
        {members.isError && (
          <ErrorBox error={members.error} onRetry={() => void members.refetch()} />
        )}
        {members.data && (
          <ul className="members">
            {members.data.map((m) => {
              const self = m.userId === user.id
              const canRemove = self ? m.role !== 'owner' : isOwner
              return (
                <li key={m.userId}>
                  <Avatar name={m.name} url={m.avatarUrl} size={36} />
                  <div className="member-info">
                    <strong>
                      {m.name}
                      {self && ' (you)'}
                    </strong>
                    <span className="muted small">{m.email}</span>
                  </div>
                  {m.role === 'owner' && <span className="badge">Owner</span>}
                  {canRemove && (
                    <button
                      type="button"
                      className="secondary small"
                      disabled={remove.isPending}
                      onClick={() => {
                        removeMember(m.userId, m.name)
                      }}
                    >
                      {self ? 'Leave' : 'Remove'}
                    </button>
                  )}
                </li>
              )
            })}
          </ul>
        )}
        {remove.isError && <p className="error">{errorMessage(remove.error)}</p>}
      </section>
      <InviteCard />
    </div>
  )
}

function InviteCard() {
  const invite = useCreateInvite()
  const [copied, setCopied] = useState(false)

  async function copy(url: string) {
    try {
      await navigator.clipboard.writeText(url)
      setCopied(true)
    } catch {
      setCopied(false) // clipboard blocked: the link is still visible to copy by hand
    }
  }

  return (
    <section className="card">
      <h2>Invite someone</h2>
      <p className="muted">
        Create a link and send it to a family member. It works once and expires in 7 days.
      </p>
      {invite.data ? (
        <div className="invite">
          <input
            readOnly
            value={invite.data.url}
            aria-label="Invite link"
            onFocus={(e) => {
              e.target.select()
            }}
          />
          <button type="button" onClick={() => void copy(invite.data.url)}>
            {copied ? 'Copied' : 'Copy'}
          </button>
          <p className="muted small">
            Expires {formatShortDate(new Date(invite.data.expiresAt))}. Anyone with the link can
            join — share it only with the intended person.
          </p>
        </div>
      ) : (
        <button
          type="button"
          disabled={invite.isPending}
          onClick={() => {
            setCopied(false)
            invite.mutate()
          }}
        >
          {invite.isPending ? 'Creating…' : 'Create invite link'}
        </button>
      )}
      {invite.isError && <p className="error">{errorMessage(invite.error)}</p>}
    </section>
  )
}
