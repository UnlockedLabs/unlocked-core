FROM ghcr.io/unlockedlabs/golang-jasper:latest

ENV GOTOOLCHAIN=auto
WORKDIR /app

RUN go install github.com/air-verse/air@v1.61.0
RUN go install github.com/go-delve/delve/cmd/dlv@latest

COPY backend/go.mod backend/go.sum ./
RUN go mod download
EXPOSE 8080
CMD ["air", "-c", ".air.toml"]
