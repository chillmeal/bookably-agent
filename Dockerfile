FROM golang:1.22-alpine AS builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /agent ./cmd/agent

FROM alpine:3.20
RUN apk --no-cache add ca-certificates && adduser -D -H -u 10001 appuser

WORKDIR /
COPY --from=builder /agent /agent
COPY --from=builder /app/prompts /prompts

USER appuser
EXPOSE 8080
ENTRYPOINT ["/agent"]
