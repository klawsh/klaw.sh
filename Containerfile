# Build stage
FROM docker.io/library/golang:1.24-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o klaw ./cmd/klaw

# Runtime stage - use alpine for CA certs and shell
FROM docker.io/library/alpine:3.20

# Install CA certificates and bash
RUN apk add --no-cache ca-certificates bash

# Copy klaw binary
COPY --from=builder /build/klaw /usr/local/bin/klaw

# Create workspace
RUN mkdir -p /workspace

WORKDIR /workspace

ENTRYPOINT ["/usr/local/bin/klaw"]
