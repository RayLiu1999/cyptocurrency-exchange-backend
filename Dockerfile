# Build Stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git make

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Which service to build (e.g., gateway, matching-engine, order-service, market-data-service)
ARG SERVICE_NAME

# Build the binary
# CGO_ENABLED=0 for static binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o main ./cmd/${SERVICE_NAME}/main.go

# Run Stage
FROM alpine:latest

WORKDIR /root/

# Install CA certificates for HTTPS/SSL
RUN apk --no-cache add ca-certificates

# Copy binary from builder
COPY --from=builder /app/main .

# Expose ports based on the service (Note: EXPOSE is mostly documentation, Docker Compose maps ports)
# 8100: order-service
# 8101: matching-engine (health)
# 8102: market-data-service
# 8103: gateway
EXPOSE 8100 8101 8102 8103

# Run the binary
ENTRYPOINT ["./main"]
