# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.26.3
ARG GOPROXY=https://goproxy.cn,direct
ARG GOSUMDB=sum.golang.google.cn

FROM golang:${GO_VERSION} AS deps
ARG GOPROXY
ARG GOSUMDB

ENV GOPROXY=${GOPROXY} \
    GOSUMDB=${GOSUMDB}

WORKDIR /src

COPY go.mod go.sum ./

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

FROM deps AS source

COPY . ./

ENV CGO_ENABLED=0 \
    GOOS=linux

FROM source AS api-build

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/api ./cmd

FROM source AS worker-build

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

FROM alpine:3.21 AS runtime

RUN sed -i 's|https://dl-cdn.alpinelinux.org/alpine|https://mirrors.aliyun.com/alpine|g' /etc/apk/repositories \
    && apk add --no-cache ca-certificates tzdata \
    && adduser -D -H -s /sbin/nologin app
WORKDIR /app

COPY --from=api-build /out/api /app/api
COPY --from=worker-build /out/worker /app/worker
COPY --from=source /src/configs ./configs

RUN mkdir -p ./.run/uploads \
    && chown -R app:app /app

USER app

EXPOSE 8080

CMD ["/app/api"]