# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.25-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum* ./
RUN if [ -f go.sum ]; then go mod download; else go mod download; fi
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/fallbakit-agent ./cmd/fallbakit-agent

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/fallbakit-agent /fallbakit-agent
USER nonroot:nonroot
ENTRYPOINT ["/fallbakit-agent"]
