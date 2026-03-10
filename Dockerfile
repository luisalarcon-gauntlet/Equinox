# Multi-stage build for Project Equinox.
# Run with your .env:  docker run --env-file .env -p 8080:8080 equinox
# Or use docker compose up (from repo root) so env_file: .env is used.
# Stage 1: build static binary.
FROM golang:1.26-alpine AS builder

WORKDIR /build

# Copy dependency manifests first for better layer caching.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build a static binary (no CGO). Embed directive in main.go includes static/.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /equinox .

# Stage 2: minimal runtime image.
FROM alpine:3.19

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=builder /equinox .

# Default port; override with SERVER_PORT env.
EXPOSE 8080

ENTRYPOINT ["/app/equinox"]
