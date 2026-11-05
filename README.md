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

- Go 1.24+
- Node.js 22+
- Docker with the Compose plugin
- [golangci-lint](https://golangci-lint.run/welcome/install/) (for linting)
- A Google OAuth client ([how to create one](https://developers.google.com/identity/openid-connect/openid-connect#appsetup))
  with the redirect URI `http://localhost:8080/auth/callback`

### Run locally

```sh
cp deploy/.env.example .env   # then fill in the OAuth client ID and secret
make db-up                    # start PostgreSQL
make migrate-up               # apply database migrations
make dev                      # start backend and frontend dev server
```

Run `make help` to see all available commands.

## Deployment

Deployment instructions will be added in [`deploy/`](deploy/) once the
production setup is in place.

## Contributing

Contributions are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) and the
[Code of Conduct](CODE_OF_CONDUCT.md) first.

## Security

Please do not report security issues in public issues. See [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE)
