# Build stage
FROM golang:1.22-alpine AS builder
RUN apk add --no-cache git
WORKDIR /app
COPY types/ /app/types/
COPY gateway/ /app/gateway/
WORKDIR /app/gateway
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/bin/gateway ./cmd/...

# Development stage (with Air hot-reload)
FROM golang:1.22-alpine AS dev
RUN apk add --no-cache git wget
RUN go install github.com/air-verse/air@latest
WORKDIR /app
COPY types/ /app/types/
COPY gateway/ /app/gateway/
WORKDIR /app/gateway
RUN go mod download
CMD ["air", "-c", ".air.toml"]

# Production stage
FROM alpine:3.19 AS prod
RUN apk add --no-cache ca-certificates wget
COPY --from=builder /app/bin/gateway /usr/local/bin/gateway
CMD ["gateway"]
