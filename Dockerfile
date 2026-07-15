# Використовується лише для CI-збірки; продовий деплой — LXC + systemd.
FROM golang:1.22-bookworm AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -o /out/oddinvestd ./cmd/oddinvestd

FROM debian:bookworm-slim
RUN useradd -r oddinvestd && mkdir -p /var/lib/oddinvestd && chown oddinvestd /var/lib/oddinvestd
COPY --from=build /out/oddinvestd /usr/local/bin/oddinvestd
USER oddinvestd
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/oddinvestd"]
