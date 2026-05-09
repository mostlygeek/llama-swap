FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY cmd/wol-proxy ./cmd/wol-proxy

ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/wol-proxy ./cmd/wol-proxy

FROM alpine:3.21

RUN apk add --no-cache ca-certificates

COPY --from=build /out/wol-proxy /usr/local/bin/wol-proxy

USER 65532:65532

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/wol-proxy"]
