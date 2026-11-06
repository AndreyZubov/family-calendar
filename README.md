# Family Calendar

[![CI](https://github.com/AndreyZubov/family-calendar/actions/workflows/ci.yml/badge.svg)](https://github.com/AndreyZubov/family-calendar/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A minimal, self-hosted calendar for families. Sign in with Google, create events,
see what's coming up and invite family members to events. Built to run on a small
VPS: a single Go binary with the React UI embedded, PostgreSQL, and Caddy for
automatic HTTPS.

> **Status:** early development. Not ready for production use yet.

## Features

- Sign in with Google (OpenID Connect, Authorization Code Flow + PKCE)
- Create, view, edit and delete events
- Add family members to an event
- Invite family members to your family

## Architecture

```
Browser ──HTTPS──▶ Caddy ──▶ Go app (API + embedded React SPA) ──▶ PostgreSQL
                               │
                               └──▶ Google (OIDC)
```

- **Backend:** Go, `net/http`, `pgx`, `sqlc`, `goose`, `go-oidc`
- **Frontend:** React, TypeScript, Vite, TanStack Query
- **Auth:** backend-for-frontend (BFF) pattern. OAuth tokens never reach the browser;
  the client only holds an `HttpOnly`, `Secure`, `SameSite=Lax` session cookie.
- **Deploy:** Docker Compose, images published to GHCR by GitHub Actions.

## Repository layout

| Path | Contents |
|---|---|
| `backend/` | Go API server, migrations, SQL queries, OpenAPI spec |
| `frontend/` | React single-page app |
| `deploy/` | Docker Compose files, Caddyfile, example environment |
| `.github/` | CI/CD workflows, issue and PR templates |

## Getting started

### Prerequisites

- Go 1.27+
- Node.js 22+
- Docker with the Compose plugin
- [golangci-lint](https://golangci-lint.run/welcome/install/) (for linting)

### Run locally

```sh
make env                # create .env (dev sign-in, generated session secret)
make frontend-install   # install frontend dependencies
make dev                # start PostgreSQL, dev sign-in provider, backend and Vite
```

Open http://localhost:5173. Out of the box, sign-in goes to a **local
development identity provider** (`backend/cmd/devoidc`): pick "Sign in as
Alice" or type any name — no Google account needed. It is accepted only in
development mode on localhost and is never part of the production image.

To use real Google sign-in locally, create an OAuth client
([how](https://developers.google.com/identity/openid-connect/openid-connect#appsetup))
with the redirect URI `http://localhost:5173/auth/callback` and set the
`OIDC_*` variables in `.env` as described there.

Migrations run automatically when the backend starts.

### Tests

```sh
make test           # backend (DB tests in Docker via testcontainers) + frontend
make e2e            # end-to-end in Chromium (first: cd frontend && npx playwright install chromium)
```

Run `make help` to see all available commands.

## Docker image

One small image (~5 MB compressed): a static Go binary with the React app
embedded, on [distroless](https://github.com/GoogleContainerTools/distroless)
(no shell, no package manager), running as a non-root user.

```sh
make docker-build   # build family-calendar:<version>
make docker-test    # build + smoke test (locked down, next to a throwaway Postgres)
```

The container serves the API and the UI on port 8080 and applies database
migrations on startup. It is configured only through environment variables —
see [`.env.example`](.env.example).

## Deployment

Production setup (Docker Compose with Caddy for HTTPS) is described in
[`deploy/`](deploy/).

## Contributing

Contributions are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) and the
[Code of Conduct](CODE_OF_CONDUCT.md) first.

## Security

Please do not report security issues in public issues. See [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE)
