ARG FFMPEG_VERSION=7.1

FROM mwader/static-ffmpeg:$FFMPEG_VERSION AS ffmpeg

FROM ghcr.io/unlockedlabs/golang-jasper:latest

ENV GOTOOLCHAIN=auto
WORKDIR /app

# The Open Content worker (`backend worker opencontent`) downloads videos with
# yt-dlp and ffmpeg. yt-dlp needs a JavaScript runtime (node) to get YouTube's
# video formats, and only uses node when its config turns it on.
RUN apk add --no-cache python3 py3-pip nodejs && \
	pip install --break-system-packages requests brotli websockets certifi yt-dlp[default,curl-cffi]
COPY --from=ffmpeg /ffmpeg /ffprobe /usr/bin/
RUN wget -qO /usr/bin/yt-dlp https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp && chmod 755 /usr/bin/yt-dlp
RUN mkdir -p /root/.config/yt-dlp && echo '--js-runtimes node' > /root/.config/yt-dlp/config

RUN go install github.com/air-verse/air@v1.61.0
RUN go install github.com/go-delve/delve/cmd/dlv@latest

COPY backend/go.mod backend/go.sum ./
RUN go mod download
EXPOSE 8080
CMD ["air", "-c", ".air.toml"]
