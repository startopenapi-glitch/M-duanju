# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/drama-server ./cmd/server

FROM alpine:3.20
ARG INSTALL_FFMPEG=false
RUN apk add --no-cache ca-certificates tzdata \
 && if [ "$INSTALL_FFMPEG" = "true" ]; then apk add --no-cache ffmpeg; fi
WORKDIR /app
COPY --from=build /out/drama-server /usr/local/bin/drama-server
COPY backend/migrations ./migrations
ENV BIND_ADDR=0.0.0.0 PORT=8080
EXPOSE 8080
ENTRYPOINT ["drama-server"]
CMD ["api"]
