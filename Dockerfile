# syntax=docker/dockerfile:1

# ---------------------------------------------------------------------------
# The admin interface, which the Go binary embeds.
#
# It has to be built before the Go stage, and that is a build-order fact rather
# than a preference: `//go:embed all:dist` resolves at compile time, so a binary
# compiled without web/dist has no interface at all. Such a binary still starts,
# still listens, and still answers every page — with a page saying the frontend
# was not built. Nothing about the container looks wrong from the outside.
#
# $BUILDPLATFORM rather than $TARGETPLATFORM: Vite's output is the same bytes on
# every architecture, so building it under emulation for an arm64 image would be
# minutes of QEMU for nothing.
#
# The two COPYs are the cache shape, not tidiness: the lockfile alone comes
# first so that editing a source file does not re-install every dependency. npm
# ci rather than npm install, so the stage installs exactly what the lockfile
# says — which is what makes the image reproducible.
#
# `COPY web/ ./` must not bring a host node_modules with it. It would overwrite
# the container's, and esbuild and rollup ship native binaries — a Windows
# checkout's would fail on linux/amd64 with an error about the wrong platform.
# .dockerignore is what prevents it; see the note there.
# ---------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM node:22-alpine AS frontend
WORKDIR /src/web

COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/ ./
RUN npm run build

# Must satisfy the go directive in go.mod. github.com/wneessen/go-mail
# requires Go >= 1.25, so a 1.24 base image cannot build this project.
FROM --platform=$BUILDPLATFORM golang:1.25 AS build
WORKDIR /src

# Pinned explicitly rather than inherited from the host: a 32-bit host
# toolchain would otherwise produce a 32-bit binary.
ARG TARGETOS=linux
ARG TARGETARCH=amd64

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# The interface, over the top of wherever the context would have put it —
# .dockerignore excludes web/dist, so there is normally nothing there and this
# is the only thing that makes the embed pattern match.
COPY --from=frontend /src/web/dist ./web/dist

# CGO_ENABLED=0 keeps the result a static binary so it can run on distroless.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w" -o /out/notifyrelay ./cmd/notifyrelay

# The data directory has to exist in the image, and it has to be owned by the
# user the service runs as.
#
# Two reasons, and the second is the one that bites. The obvious one is that a
# container started without a volume needs somewhere to write. The subtle one
# is that when a *named volume* is mounted at a path that exists in the image,
# Docker seeds the volume with that directory's contents and ownership — so
# this is what makes a fresh volume writable by the nonroot user instead of by
# root. A directory created only at runtime, or by WORKDIR, is owned by root
# and the service fails at startup with a permission error.
#
# distroless has no shell, so this cannot be fixed in the final stage with RUN.
RUN mkdir -p /out/app/data


FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/notifyrelay /notifyrelay
COPY --from=build /src/NOTICE /NOTICE
COPY --from=build /src/LICENSE /LICENSE

# A working configuration, so the image runs without one being written first.
# It sets no secrets: both keys are generated into the data directory on first
# boot, and the administrator is created through the UI. Mount your own file
# over this path to change anything.
COPY --from=build /src/deploy/docker/config.yaml /etc/notifyrelay/config.yaml

# 65532 is the nonroot user in the distroless base. Numeric because the final
# stage has no /etc/passwd entry to resolve a name against.
COPY --chown=65532:65532 --from=build /out/app /app

# The working directory is where the database and the spool are written, and
# the shipped configuration names them with relative paths
# (`data/notifyrelay.db`, `data/spool`). With this set, the example
# configuration works unmodified, with the volume mounted at /app/data.
#
# WORKDIR alone would not be enough: Docker creates it owned by root, because
# the USER below has not taken effect yet. The /app/data copy above is what
# makes the directory writable.
WORKDIR /app

USER nonroot:nonroot

EXPOSE 8080 2525

# The image is distroless: no shell, no curl, no wget. A container healthcheck
# therefore has nothing to run except this binary, which is what --healthcheck
# is for. Compose and Kubernetes both use it.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/notifyrelay", "--healthcheck", "127.0.0.1:8080"]

ENTRYPOINT ["/notifyrelay"]
CMD ["--config", "/etc/notifyrelay/config.yaml"]
