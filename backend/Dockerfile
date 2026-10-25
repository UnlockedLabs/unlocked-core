ARG FFMPEG_VERSION=7.1

FROM mwader/static-ffmpeg:$FFMPEG_VERSION AS ffmpeg

FROM ghcr.io/unlockedlabs/golang-jasper:latest AS builder

ENV GOTOOLCHAIN=auto
WORKDIR /app
COPY ./backend/go.mod ./backend/go.sum ./
RUN go mod download

COPY ./backend/ ./

RUN CGO_ENABLED=0 GOOS=linux GOFLAGS=-mod=readonly go build -trimpath -o backend ./cmd/main.go
RUN cd migrations && CGO_ENABLED=0 GOOS=linux GOFLAGS=-mod=readonly go build -trimpath -o /app/migrate .

FROM ghcr.io/unlockedlabs/golang-jasper:latest
WORKDIR /

# The Open Content worker (`backend worker opencontent`) downloads videos with
# yt-dlp and ffmpeg. yt-dlp needs a JavaScript runtime (node) to get YouTube's
# video formats, and only uses node when its config turns it on.
RUN apk add --no-cache python3 py3-pip nodejs && \
	pip install --break-system-packages requests brotli websockets certifi yt-dlp[default,curl-cffi]
COPY --from=ffmpeg /ffmpeg /ffprobe /usr/bin/
RUN wget -qO /usr/bin/yt-dlp https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp && chmod 755 /usr/bin/yt-dlp
RUN mkdir -p /root/.config/yt-dlp && echo '--js-runtimes node' > /root/.config/yt-dlp/config

# Records which commit/branch/version this image was actually built from — see
# /api/version. Passed in by `make dev` (local git state) or CI (github.sha/ref_name/
# the release tag); "unknown" for anyone who builds without them.
ARG GIT_COMMIT=unknown
ARG GIT_BRANCH=unknown
ARG APP_VERSION=unknown
ENV GIT_COMMIT=$GIT_COMMIT
ENV GIT_BRANCH=$GIT_BRANCH
ENV APP_VERSION=$APP_VERSION

COPY --from=builder /app/backend /backend
COPY --from=builder /app/migrate /migrate
COPY --from=builder /app/src/templates /templates
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /app/migrations /migrations
COPY --from=builder /usr/local/go/lib/time/zoneinfo.zip /
ENV ZONEINFO=/zoneinfo.zip

EXPOSE 8080
ENTRYPOINT ["/backend"]
