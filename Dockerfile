# syntax=docker/dockerfile:1
FROM golang:1.26-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -buildvcs=false -trimpath -ldflags='-s -w' -o /out/ai-quota-frame ./cmd/ai-quota-frame

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates chromium \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/ai-quota-frame /usr/local/bin/ai-quota-frame
USER nobody
ENTRYPOINT ["/usr/local/bin/ai-quota-frame"]
