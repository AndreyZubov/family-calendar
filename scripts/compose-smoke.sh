#!/usr/bin/env bash
# Smoke test of the production Compose stack (deploy/docker-compose.yml) on
# this machine: Caddy with HTTPS (its internal CA for "localhost"), the app
# image, and Postgres — with the real hardening settings.
#
# Usage: scripts/compose-smoke.sh [image-tag]   (default: dev → family-calendar:dev)
# Needs Docker and internet access (the app discovers Google's OIDC
# configuration at startup). Uses ports 18080/18443 and project "fc-smoke".
set -euo pipefail
cd "$(dirname "$0")/../deploy"

TAG="${1:-dev}"
PROJECT=fc-smoke
ENV_FILE="$(mktemp)"
failures=0
pass() { printf '  \033[32m✓\033[0m %s\n' "$1"; }
fail() { printf '  \033[31m✗\033[0m %s\n' "$1"; failures=$((failures + 1)); }
check() { if eval "$2"; then pass "$1"; else fail "$1"; fi; }

cat >"$ENV_FILE" <<ENV
DOMAIN=localhost
ACME_EMAIL=smoke@example.com
APP_IMAGE=family-calendar
APP_VERSION=$TAG
POSTGRES_PASSWORD=$(openssl rand -hex 24)
OIDC_CLIENT_ID=smoke.apps.googleusercontent.com
OIDC_CLIENT_SECRET=smoke
SESSION_SECRET=$(openssl rand -base64 32)
HTTP_PORT=18080
HTTPS_PORT=18443
ENV
dc() { docker compose -p "$PROJECT" --env-file "$ENV_FILE" "$@"; }
cleanup() { dc down -v --remove-orphans >/dev/null 2>&1 || true; rm -f "$ENV_FILE"; }
trap cleanup EXIT

echo "Starting stack (image family-calendar:$TAG)"
dc up -d --wait --wait-timeout 120 >/dev/null 2>&1 || { dc ps; dc logs --tail 30; exit 1; }
B=https://localhost:18443
c() { curl -sk --max-time 10 "$@"; }

echo "Containers"
for s in caddy app postgres; do
  id=$(dc ps -q "$s")
  check "$s is running" '[[ "$(docker inspect -f "{{.State.Status}}" "$id")" == running ]]'
  check "$s has a read-only root filesystem" '[[ "$(docker inspect -f "{{.HostConfig.ReadonlyRootfs}}" "$id")" == true ]]'
  check "$s has no-new-privileges and dropped capabilities" 'docker inspect -f "{{.HostConfig.SecurityOpt}} {{.HostConfig.CapDrop}}" "$id" | grep -q "no-new-privileges.*ALL"'
done
check "app is healthy" '[[ "$(docker inspect -f "{{.State.Health.Status}}" "$(dc ps -q app)")" == healthy ]]'
check "postgres is healthy" '[[ "$(docker inspect -f "{{.State.Health.Status}}" "$(dc ps -q postgres)")" == healthy ]]'

echo "HTTPS through Caddy"
check "https /healthz 200" '[[ "$(c -o /dev/null -w "%{http_code}" "$B/healthz")" == 200 ]]'
check "served over HTTP/2" '[[ "$(c -o /dev/null -w "%{http_version}" "$B/healthz")" == 2 ]]'
headers=$(c -D - -o /dev/null -H "Accept-Encoding: zstd, gzip" "$B/")
check "app security headers pass through (CSP, HSTS)" 'grep -qi "^content-security-policy:" <<<"$headers" && grep -qi "^strict-transport-security:" <<<"$headers"'
check "no Server banner" '! grep -qi "^server:" <<<"$headers"'
check "responses are compressed" 'grep -qiE "^content-encoding: (zstd|gzip)" <<<"$headers"'
check "plain http redirects to https" 'curl -s -o /dev/null -w "%{http_code} %{redirect_url}" http://localhost:18080/ | grep -qE "^30[18] https://"'
check "API answers through the proxy" '[[ "$(c -o /dev/null -w "%{http_code}" "$B/api/me")" == 401 ]]'

echo "Proxy trust"
c -o /dev/null -H "X-Forwarded-For: 6.6.6.6" "$B/api/me"
sleep 1
logged_ip=$(dc logs app 2>/dev/null | grep '"path":"/api/me"' | tail -1 | grep -oE '"client_ip":"[^"]*"' | cut -d'"' -f4)
check "app logs the real client, not Caddy or a spoofed header ($logged_ip)" '[[ -n "$logged_ip" && "$logged_ip" != 172.30.10.10 && "$logged_ip" != 6.6.6.6 ]]'

echo "Network isolation"
check "postgres publishes no port" '[[ -z "$(docker inspect -f "{{json .NetworkSettings.Ports}}" "$(dc ps -q postgres)" | grep -o HostPort)" ]]'
check "postgres cannot reach the internet" '! docker exec "$(dc ps -q postgres)" wget -q -T 3 -O /dev/null https://example.com 2>/dev/null'
check "caddy cannot reach postgres (different network)" '! docker exec "$(dc ps -q caddy)" nc -z -w 2 postgres 5432 2>/dev/null'

echo "Resources"
docker stats --no-stream --format '    {{.Name}}: {{.MemUsage}}' $(dc ps -q)

echo "Restart keeps data"
docker exec "$(dc ps -q postgres)" psql -U calendar -d calendar -qtAc "INSERT INTO families (name) VALUES ('smoke-persist')" >/dev/null
dc restart app postgres >/dev/null 2>&1
dc up -d --wait --wait-timeout 60 >/dev/null 2>&1
check "data survives a restart" '[[ "$(docker exec "$(dc ps -q postgres)" psql -U calendar -d calendar -qtAc "SELECT count(*) FROM families WHERE name = '"'"'smoke-persist'"'"'")" == 1 ]]'
check "app healthy again after restart" '[[ "$(c -o /dev/null -w "%{http_code}" "$B/readyz")" == 200 ]]'

echo
if ((failures)); then echo "FAILED: $failures check(s)"; exit 1; fi
echo "All checks passed"
