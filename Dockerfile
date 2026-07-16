# Multi-stage production image: static-ish binary, non-root runtime.
FROM golang:1.24.4-bookworm AS build

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
	&& apt-get install -y --no-install-recommends ca-certificates \
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

ENTRYPOINT ["/usr/local/bin/goroku"]
CMD ["--data-root", "/data", "--no-git", "--port", "8080"]
