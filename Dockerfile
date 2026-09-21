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

USER nonroot:nonroot

EXPOSE 8080 2525

ENTRYPOINT ["/notifyrelay"]
CMD ["--config", "/etc/notifyrelay/config.yaml"]
