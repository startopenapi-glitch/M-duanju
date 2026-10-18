# syntax=docker/dockerfile:1
FROM golang:1.23-alpine AS build
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/drama-server ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates ffmpeg tzdata \
    && addgroup -S drama && adduser -S drama -G drama
WORKDIR /app
COPY --from=build /out/drama-server /usr/local/bin/drama-server
COPY migrations ./migrations
RUN mkdir -p /data/storage && chown -R drama:drama /data /app
USER drama
ENV STORAGE_DIR=/data/storage
ENTRYPOINT ["drama-server"]
CMD ["api"]
