# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Copy the go.mod and source code
COPY go.mod ./
COPY main.go ./

# Build the Go application
RUN CGO_ENABLED=0 GOOS=linux go build -o chaos-monkey main.go

# Final stage
FROM alpine:3.18

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /app/chaos-monkey .

# Run the binary
CMD ["./chaos-monkey"]
