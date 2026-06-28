FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
COPY internal ./internal
COPY cmd ./cmd
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/sre-agent ./cmd/sre-agent

FROM alpine:3.20
WORKDIR /app
COPY --from=builder /out/sre-agent /usr/local/bin/sre-agent
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/sre-agent"]
