# syntax=docker/dockerfile:1
#
# Production image: one static Go binary with the React app embedded,
# on a distroless base (no shell, no package manager), running as non-root.
#
#   docker build -t family-calendar .
#
# Base images are pinned by version and digest (tags can be moved; digests
# cannot). Dependabot proposes updates.

# ---- 1. Frontend: install exactly the locked dependencies and build ----------
FROM node:24.22.2-alpine@sha256:a34ab6d7f6f4813f4d0c5469d74685604b962fbdbf5793a7bc7b4998ecb867c8 AS frontend
WORKDIR /src/frontend
# Dependencies first: this layer is cached until package*.json change.
COPY frontend/package.json frontend/package-lock.json frontend/.npmrc ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

# ---- 2. Backend: compile a static binary with the frontend embedded ----------
FROM golang:1.27.3-alpine@sha256:fa3d596898d9f126af7c01d14ee7597bab3f39cccf392ef36b69fe77cd681f18 AS backend
WORKDIR /src/backend
# CGO off: a fully static binary that runs on distroless/static.
# GOTOOLCHAIN=local: build with this image's Go, never download another.
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
# No separate `go mod download`: it would fetch every module in go.mod,
# including test-only ones (testcontainers, Docker client). `go build` fetches
# just what the server imports; the cache mount keeps them between builds.
COPY backend/ ./
COPY --from=frontend /src/frontend/dist/ ./internal/web/dist/
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/server ./cmd/server

# ---- 3. Runtime --------------------------------------------------------------
# distroless/static: CA certificates (for HTTPS to Google), tzdata, /etc/passwd
# with a "nonroot" user (uid 65532). Nothing else.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329

ARG VERSION=dev
LABEL org.opencontainers.image.title="family-calendar" \
      org.opencontainers.image.description="Minimal self-hosted family calendar" \
      org.opencontainers.image.source="https://github.com/AndreyZubov/family-calendar" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}"

COPY --from=backend /out/server /server

ENV APP_ENV=production \
    HTTP_ADDR=:8080
USER nonroot:nonroot
EXPOSE 8080

# No curl in distroless: the binary checks its own /healthz.
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD ["/server", "healthcheck"]

ENTRYPOINT ["/server"]
