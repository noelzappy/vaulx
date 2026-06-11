# ── Stage 1: Build ──────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

# Install templ at the version pinned in go.mod
RUN go install github.com/a-h/templ/cmd/templ@v0.3.1020

# Cache dependencies before copying source
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Generate templ components, then compile the server binary
RUN templ generate
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o server ./cmd/server

# ── Stage 2: Runtime ─────────────────────────────────────────────────────────
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /app/server .
COPY --from=builder /app/web/static ./web/static

EXPOSE 8080

CMD ["./server"]
