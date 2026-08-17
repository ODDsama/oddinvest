# Використовується лише для CI-збірки; продовий деплой — LXC + systemd.
#
# Версія тут — ЄДИНА в репозиторії, яку доводиться тримати руками: у FROM
# не підставиш значення з файлу. Скрипти розгортання більше констант не
# мають — вони читають директиву `go` з go.mod, а CI бере її звідти ж через
# go-version-file. Тож при піднятті мінімуму в go.mod правити треба рівно
# цей рядок.
#
# Що буває, коли цього не зробити, уже перевірено на живому: go.mod поїхав
# на 1.24, обидва середовища збірки лишились на 1.23, а GOTOOLCHAIN=local
# забороняє довантажити потрібний — розгортання падало на
# «go.mod requires go >= 1.24.0» і тихо лишало старий бінарник.
FROM golang:1.24-bookworm AS build
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
