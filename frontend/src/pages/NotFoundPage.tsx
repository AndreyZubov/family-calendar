import { Link } from 'react-router'

export function NotFoundPage() {
  return (
    <section className="status">
      <h1>Page not found</h1>
      <Link to="/">Back to the calendar</Link>
    </section>
  )
}
