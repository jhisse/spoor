# CGO_ENABLED=0 works because modernc.org/sqlite is pure Go: the binary is
# statically linked, with no libc dependency. The build stage always runs on
# the builder's own architecture and cross-compiles, so a multi-arch image
# needs no emulation.
FROM --platform=$BUILDPLATFORM golang:1.27.1 AS builder
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN mkdir /data
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /spoor ./cmd/spoor

# distroless/static:nonroot: no shell, no package manager, uid 65532. A
# mounted database directory must be writable by that uid.
FROM gcr.io/distroless/static-debian12@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=builder /spoor /spoor
# An empty /data owned by nonroot: a named volume mounted there inherits it.
COPY --from=builder --chown=65532:65532 /data /data
# spoor binds loopback by default: neither the UI nor ingest has
# authentication. Inside a container loopback is unreachable from the host,
# so the image listens on every interface; what is exposed is decided by
# `docker run -p`. Publish both ports on loopback (-p 127.0.0.1:8080:8080
# -p 127.0.0.1:4318:4318) unless a boundary you control is in front.
ENV SPOOR_HTTP_ADDR=:8080 SPOOR_INGEST_ADDR=:4318
ENTRYPOINT ["/spoor"]
CMD ["serve"]
