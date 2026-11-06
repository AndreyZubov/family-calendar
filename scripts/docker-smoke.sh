#!/usr/bin/env bash
# Smoke test of the production Docker image: runs it next to a throwaway
# Postgres, locked down as in production (read-only filesystem, no Linux
# capabilities, no privilege escalation), and checks what users and the
# deploy depend on.
#
# Usage: scripts/docker-smoke.sh [image]   (default: family-calendar:dev)
# Needs Docker, curl and internet access (production mode discovers Google's
# OIDC configuration at startup).
set -euo pipefail

IMAGE="${1:-family-calendar:dev}"
ID="fc-smoke-$$"
NET="$ID-net"
DB="$ID-db"
APP="$ID-app"
failures=0

pass() { printf '  \033[32m✓\033[0m %s\n' "$1"; }
fail() { printf '  \033[31m✗\033[0m %s\n' "$1"; failures=$((failures + 1)); }
check() { if eval "$2"; then pass "$1"; else fail "$1"; fi; }

cleanup() {
  docker rm -f "$APP" "$DB" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "Image: $IMAGE"
docker network create "$NET" >/dev/null
docker run -d --name "$DB" --network "$NET" \
  -e POSTGRES_USER=calendar -e POSTGRES_PASSWORD=calendar -e POSTGRES_DB=calendar \
  postgres:17-alpine >/dev/null
for _ in $(seq 1 60); do
  docker exec "$DB" pg_isready -U calendar -d calendar >/dev/null 2>&1 && break
  sleep 1
done

# Same HEALTHCHECK command as in the Dockerfile, just probed every 2 s so the
# test does not wait for the 30 s production interval.
docker run -d --name "$APP" --network "$NET" -p 127.0.0.1::8080 \
  --read-only --cap-drop ALL --security-opt no-new-privileges \
  --health-interval=2s --health-start-period=1s \
  -e APP_BASE_URL=https://calendar.example.com \
  -e DATABASE_URL="postgres://calendar:calendar@$DB:5432/calendar?sslmode=disable" \
  -e OIDC_CLIENT_ID=smoke.apps.googleusercontent.com -e OIDC_CLIENT_SECRET=smoke \
  -e SESSION_SECRET="$(head -c 32 /dev/urandom | base64)" \
  "$IMAGE" >/dev/null
PORT=$(docker port "$APP" 8080/tcp | head -1 | sed 's/.*://')
B="http://127.0.0.1:$PORT"

echo "Startup"
status=""
for _ in $(seq 1 60); do
  status=$(docker inspect -f '{{.State.Health.Status}}' "$APP" 2>/dev/null || echo gone)
  [[ "$status" == healthy || "$status" == unhealthy || "$status" == gone ]] && break
  sleep 1
done
check "Docker HEALTHCHECK reports healthy (status: $status)" '[[ "$status" == healthy ]]'
if [[ "$status" != healthy ]]; then docker logs "$APP" | tail -20; exit 1; fi
check "logs are JSON and migrations ran" 'docker logs "$APP" 2>&1 | grep -q "\"msg\":\"migrations up to date\""'
check "process runs as nonroot (uid 65532)" '[[ "$(docker top "$APP" -o pid,uid | awk "NR==2{print \$2}")" == 65532 ]]'

echo "Health and API"
check "/healthz 200 with version" 'curl -sf "$B/healthz" | grep -q "\"status\":\"ok\""'
check "/readyz 200 (database reachable)" 'curl -sf "$B/readyz" >/dev/null'
check "/api/me 401 JSON without session" '[[ "$(curl -s -o /dev/null -w "%{http_code}" "$B/api/me")" == 401 ]]'
check "/auth/login redirects to Google with PKCE" 'curl -s -o /dev/null -w "%{redirect_url}" "$B/auth/login" | grep -q "accounts.google.com.*code_challenge_method=S256"'

echo "Embedded frontend"
headers=$(curl -s -D - -o /tmp/$ID-index.html "$B/")
check "/ serves index.html" 'grep -q "<div id=\"root\">" /tmp/$ID-index.html'
check "index.html is not cached (no-cache)" 'grep -qi "^cache-control: no-cache" <<<"$headers"'
check "CSP header on the page" 'grep -qi "^content-security-policy: default-src .self." <<<"$headers"'
check "HSTS header (production, https)" 'grep -qi "^strict-transport-security: max-age=" <<<"$headers"'
asset=$(grep -o '/assets/[^"]*\.js' /tmp/$ID-index.html | head -1)
check "hashed asset $asset is cached forever" 'curl -s -D - -o /dev/null "$B$asset" | grep -qi "^cache-control: public, max-age=31536000, immutable"'
check "client route /events/123 falls back to index.html" 'curl -sf "$B/events/123" | grep -q "<div id=\"root\">"'
check "missing asset is a real 404" '[[ "$(curl -s -o /dev/null -w "%{http_code}" "$B/assets/missing.js")" == 404 ]]'
rm -f /tmp/$ID-index.html

echo "Shutdown"
docker stop -t 20 "$APP" >/dev/null
check "graceful stop: exit code 0" '[[ "$(docker inspect -f "{{.State.ExitCode}}" "$APP")" == 0 ]]'
check "graceful stop: logged \"stopped\"" 'docker logs "$APP" 2>&1 | grep -q "\"msg\":\"stopped\""'

echo
if ((failures)); then echo "FAILED: $failures check(s)"; exit 1; fi
echo "All checks passed"
