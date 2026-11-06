import { NavLink, Outlet, useNavigate } from 'react-router'

import { useLogout } from '../api/queries'
import { Avatar } from './Avatar'
import { useCurrentUser } from './useCurrentUser'

export function Layout() {
  const { user, family } = useCurrentUser()
  const logout = useLogout()
  const navigate = useNavigate()

  return (
    <div className="app">
      <header className="topbar">
        <NavLink to="/" className="brand">
          {family?.name ?? 'Family Calendar'}
        </NavLink>
        {family && (
          <nav aria-label="Main">
            <NavLink to="/" end>
              Calendar
            </NavLink>
            <NavLink to="/family">Family</NavLink>
          </nav>
        )}
        <div className="user">
          <Avatar name={user.name} url={user.avatarUrl} />
          <span className="user-name">{user.name}</span>
          <button
            type="button"
            className="secondary small"
            disabled={logout.isPending}
            onClick={() => {
              logout.mutate(undefined, { onSuccess: () => void navigate('/login') })
            }}
          >
            Sign out
          </button>
        </div>
      </header>
      <main className="page">
        <Outlet />
      </main>
    </div>
  )
}
