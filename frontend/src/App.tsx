import { Route, Routes } from 'react-router'

import { RequireFamily, RequireUser } from './components/Gates'
import { Layout } from './components/Layout'
import { CalendarPage } from './pages/CalendarPage'
import { EditEventPage, NewEventPage } from './pages/EventPage'
import { FamilyPage } from './pages/FamilyPage'
import { InvitePage } from './pages/InvitePage'
import { LoginPage } from './pages/LoginPage'
import { NotFoundPage } from './pages/NotFoundPage'

/**
 * Routes:
 *   /login              public
 *   /invite#<token>     public; asks to sign in if needed
 *   everything else     signed-in users (RequireUser) with a family
 *                       (RequireFamily shows family setup otherwise)
 */
export function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/invite" element={<InvitePage />} />
      <Route element={<RequireUser />}>
        <Route element={<Layout />}>
          <Route element={<RequireFamily />}>
            <Route index element={<CalendarPage />} />
            <Route path="/family" element={<FamilyPage />} />
            <Route path="/events/new" element={<NewEventPage />} />
            <Route path="/events/:id" element={<EditEventPage />} />
          </Route>
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Route>
    </Routes>
  )
}
