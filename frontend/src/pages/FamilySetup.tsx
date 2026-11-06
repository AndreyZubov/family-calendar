import { useState, type SubmitEvent } from 'react'

import { errorMessage, fieldErrors } from '../api/errors'
import { useCreateFamily } from '../api/queries'

/** Shown to signed-in users who are not in a family yet. */
export function FamilySetup() {
  const [name, setName] = useState('')
  const create = useCreateFamily()
  const nameError = fieldErrors(create.error).name

  function submit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault()
    create.mutate(name)
  }

  return (
    <section className="card narrow">
      <h1>Create your family</h1>
      <p className="muted">
        A family shares one calendar. You can invite the others once it is created.
      </p>
      <form onSubmit={submit} noValidate>
        <label>
          Family name
          <input
            value={name}
            onChange={(e) => {
              setName(e.target.value)
            }}
            placeholder="The Smiths"
            maxLength={100}
            required
            aria-invalid={nameError ? true : undefined}
            autoFocus
          />
        </label>
        {nameError && <p className="field-error">Name {nameError}</p>}
        {create.isError && !nameError && <p className="error">{errorMessage(create.error)}</p>}
        <button type="submit" disabled={create.isPending || name.trim() === ''}>
          {create.isPending ? 'Creating…' : 'Create family'}
        </button>
      </form>
      <p className="muted small">
        Got an invite link from a relative? Open it instead to join their family.
      </p>
    </section>
  )
}
