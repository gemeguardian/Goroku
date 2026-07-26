# Multi-stage production image: static-ish binary, non-root runtime.
FROM golang:1.25.0-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG VERSION=1.0.0
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build -trimpath \
	-ldflags "-s -w -X goroku/goroku.VersionInfo=${VERSION} -X goroku/goroku.Commit=${COMMIT}" \
	-o /out/goroku .

FROM debian:bookworm-slim

RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates curl \
	&& rm -rf /var/lib/apt/lists/* \
	&& groupadd --system --gid 1000 goroku \
	&& useradd --system --uid 1000 --gid goroku --home-dir /data --shell /usr/sbin/nologin goroku \
	&& mkdir -p /data \
	&& chown goroku:goroku /data

COPY --from=build /out/goroku /usr/local/bin/goroku

ENV DOCKER=1 \
	GOROKU_NO_GIT=1

WORKDIR /data
USER goroku:goroku
EXPOSE 8080
VOLUME ["/data"]

# Readiness, not liveness: /healthz is deliberately static ("the process answers
# HTTP"), so it stays 200 with a dead MTProto connection and the container would
# look healthy while the bot is deaf. /readyz answers 200 during onboarding and
# 503 once setup has completed with no client connected, which is the state an
# orchestrator should act on. Registered in goroku/web/routes.go.
# Port must match CMD --port (default 8080). curl is installed in the runtime image.
# start-period allows cold start / first-time setup before probes count as failures.
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
	CMD curl -fsS http://127.0.0.1:8080/readyz || exit 1

ENTRYPOINT ["/usr/local/bin/goroku"]
CMD ["--data-root", "/data", "--no-git", "--port", "8080"]
