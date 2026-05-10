# syntax=docker/dockerfile:1

FROM golang:1.24-alpine AS builder

WORKDIR /src

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
COPY vendor ./vendor
COPY cmd ./cmd
COPY gen ./gen
COPY internal ./internal
COPY proto ./proto
COPY third_party ./third_party

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags="-s -w" -o /bin/blog ./cmd/blog

FROM alpine:3.21

WORKDIR /app

RUN apk add --no-cache ca-certificates && adduser -D -H -u 10001 appuser

COPY --from=builder /bin/blog /app/blog
COPY cmd/blog/swagger_ui.html /app/cmd/blog/swagger_ui.html
COPY docs ./docs

USER appuser

EXPOSE 8080 8090

ENTRYPOINT ["/app/blog"]
