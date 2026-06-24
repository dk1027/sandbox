# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Copy the go.mod and go.sum files
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the Go application
RUN CGO_ENABLED=0 GOOS=linux go build -o chaos-monkey main.go

# Final stage
FROM alpine:3.18

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /app/chaos-monkey .

# Run the binary
CMD ["./chaos-monkey"]
