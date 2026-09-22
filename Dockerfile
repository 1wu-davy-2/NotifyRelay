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
