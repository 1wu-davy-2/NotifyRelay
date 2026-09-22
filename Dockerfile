# syntax=docker/dockerfile:1

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

# CGO_ENABLED=0 keeps the result a static binary so it can run on distroless.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w" -o /out/notifyrelay ./cmd/notifyrelay


FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/notifyrelay /notifyrelay
COPY --from=build /src/NOTICE /NOTICE
COPY --from=build /src/LICENSE /LICENSE

# The working directory is where the database and the spool are written, and
# the shipped configuration names them with relative paths
# (`data/notifyrelay.db`, `data/spool`).
#
# It has to be a directory the nonroot user can write to. Left at "/" — which
# is what a container with no WORKDIR gets — the service fails on first run,
# because the database is created at startup and "/" belongs to root. Setting
# it here is what lets the example configuration work unmodified, with the
# data volume mounted at /app/data.
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
