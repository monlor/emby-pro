FROM golang:1.25.0-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/strm-sync ./cmd/strm-sync

FROM lscr.io/linuxserver/emby:latest

COPY --from=builder /out/strm-sync /usr/local/bin/strm-sync
COPY --chmod=755 rootfs/etc/services.d/strm-sync/run /etc/services.d/strm-sync/run
COPY --chmod=755 rootfs/etc/cont-init.d/10-emby-pro /etc/cont-init.d/10-emby-pro

HEALTHCHECK --interval=30s --timeout=5s --retries=3 CMD ps -eo args | grep -q '[s]trm-sync' || exit 1

EXPOSE 8096 28096
