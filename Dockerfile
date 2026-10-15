FROM node:24-alpine AS frontend
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/file-station ./cmd/file-station

FROM alpine:3.23 AS runtime
RUN apk add --no-cache ca-certificates su-exec \
    && addgroup -g 1000 file-station \
    && adduser -D -H -u 1000 -G file-station file-station \
    && mkdir -p /var/lib/file-station \
    && chown 1000:1000 /var/lib/file-station \
    && chmod 0700 /var/lib/file-station
COPY --from=backend /out/file-station /usr/local/bin/file-station
COPY --from=frontend /src/web/dist/ /usr/share/file-station/
COPY --chmod=0755 docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["-static", "/usr/share/file-station"]
