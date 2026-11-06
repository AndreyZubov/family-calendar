# Deployment

| File | Purpose |
|---|---|
| `docker-compose.yml` | Production stack: Caddy (HTTPS) → app → Postgres |
| `Caddyfile` | TLS with automatic Let's Encrypt certificates, compression, reverse proxy |
| `.env.example` | Production settings template — copy to `.env` on the server |
| `docker-compose.dev.yml` | Local development database only (`make db-up`) |

## Run on a server

Requirements: Docker with the Compose plugin, a domain whose DNS A record
points to the server, ports 80 and 443 open, and a Google OAuth client with the
redirect URI `https://<your domain>/auth/callback`.

```sh
cd deploy
cp .env.example .env && chmod 600 .env   # fill in every value
docker compose up -d --wait
docker compose ps
```

Caddy requests the certificate on first start. Migrations run when the app
starts. Update to a new release by changing `APP_VERSION` in `.env` and running
`docker compose up -d --wait` again.

## What the stack enforces

- Only Caddy publishes ports. Postgres is on an internal network with no
  internet access and no published port.
- All containers: read-only root filesystem, `no-new-privileges`, all Linux
  capabilities dropped (only the few each one needs are added back), memory
  and process limits, log rotation (3 × 10 MB).
- The app trusts `X-Forwarded-For` only from Caddy's fixed address.
- Images are pinned by version and digest.

Typical memory use: app ~20 MB, Caddy ~17 MB, Postgres ~21 MB.

The Caddy admin API is disabled: after editing the `Caddyfile`, run
`docker compose restart caddy`.

## Test the stack locally

```sh
make compose-test   # builds the image and runs scripts/compose-smoke.sh
```

It starts the stack with `DOMAIN=localhost` (Caddy then uses its own internal
CA instead of Let's Encrypt) on ports 18080/18443 and checks HTTPS, headers,
proxy trust, network isolation, hardening and persistence.
