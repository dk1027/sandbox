FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
COPY internal ./internal
COPY cmd ./cmd
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/command-center ./cmd/command-center

FROM alpine:3.20
WORKDIR /app
COPY --from=builder /out/command-center /usr/local/bin/command-center
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/command-center"]
