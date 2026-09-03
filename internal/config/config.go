// Package config — конфігурація через змінні середовища (LXC + systemd:
// значення задаються в unit-файлі через Environment=).
//
// Секретів застосунку тут НЕМАЄ, і це рішення. Пароль входу й токен для
// Home Assistant жили тут рівно один день: щоб поставити замок, треба було
// SSH у контейнер, а щоб змінити пароль — SSH і рестарт демона. Тепер вони
// в таблиці `secrets` (міграція 0053), задаються з самого застосунку й
// скидаються командою `oddinvestd -reset-auth`. Довід цілком — у шапці
// internal/api/auth.go.
package config

import "os"

type Config struct {
	HTTPAddr string // ODDINVEST_HTTP_ADDR, типово :8080
	// HTTPSAddr — другий слухач, із сертифікатом Let's Encrypt на імʼя
	// тунелю. Потрібен рівно для того, щоб та сама адреса працювала ВДОМА,
	// повз Cloudflare (internal/tunnel/cert.go). Порожньо = не слухати.
	HTTPSAddr  string // ODDINVEST_HTTPS_ADDR, типово :443
	DBPath     string // ODDINVEST_DB_PATH, типово /var/lib/oddinvestd/oddinvest.db
	MQTTAddr   string // ODDINVEST_MQTT_ADDR, tcp://host:1883 (порожньо = MQTT вимкнено)
	MQTTUser   string // ODDINVEST_MQTT_USER
	MQTTPass   string // ODDINVEST_MQTT_PASS
	MQTTPrefix string // ODDINVEST_MQTT_PREFIX, типово oddinvest
	NBUBase    string // ODDINVEST_NBU_BASE, для тестів/проксі
	// ACMEURL — каталог ACME; порожньо = бойовий Let's Encrypt. Існує
	// заради його ж лімітів: на тестовому каталозі
	// (https://acme-staging-v02.api.letsencrypt.org/directory) можна
	// пробувати скільки завгодно, і саме там перевіряють налаштування,
	// перш ніж витрачати спроби бойового.
	ACMEURL string // ODDINVEST_ACME_URL
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func Load() Config {
	return Config{
		HTTPAddr:   env("ODDINVEST_HTTP_ADDR", ":8080"),
		HTTPSAddr:  env("ODDINVEST_HTTPS_ADDR", ":443"),
		DBPath:     env("ODDINVEST_DB_PATH", "/var/lib/oddinvestd/oddinvest.db"),
		MQTTAddr:   env("ODDINVEST_MQTT_ADDR", ""),
		MQTTUser:   env("ODDINVEST_MQTT_USER", ""),
		MQTTPass:   env("ODDINVEST_MQTT_PASS", ""),
		MQTTPrefix: env("ODDINVEST_MQTT_PREFIX", "oddinvest"),
		NBUBase:    env("ODDINVEST_NBU_BASE", ""),
		ACMEURL:    env("ODDINVEST_ACME_URL", ""),
	}
}
