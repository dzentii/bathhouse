# syntax=docker/dockerfile:1
FROM golang:1.27.4-alpine AS build
ENV GOMAXPROCS=2 GOFLAGS=-p=1
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    for service in catalog booking payment identity; do \
      CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/${service} ./services/${service}/cmd || exit 1; \
    done && \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/migrate ./cmd/migrate && \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/replay ./cmd/replay
FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 app
ARG SERVICE=booking
WORKDIR /app
COPY --from=build /out/${SERVICE} /app/service
COPY --from=build /out/migrate /out/replay /app/
COPY services/${SERVICE}/migrations/ /app/migrations/
USER 10001
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=6 \
 CMD wget -q -O /dev/null http://127.0.0.1:8080/readyz || exit 1
ENTRYPOINT ["/app/service"]
