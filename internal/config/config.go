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
	HTTPAddr   string // ODDINVEST_HTTP_ADDR, типово :8080
	DBPath     string // ODDINVEST_DB_PATH, типово /var/lib/oddinvestd/oddinvest.db
	MQTTAddr   string // ODDINVEST_MQTT_ADDR, tcp://host:1883 (порожньо = MQTT вимкнено)
	MQTTUser   string // ODDINVEST_MQTT_USER
	MQTTPass   string // ODDINVEST_MQTT_PASS
	MQTTPrefix string // ODDINVEST_MQTT_PREFIX, типово oddinvest
	NBUBase    string // ODDINVEST_NBU_BASE, для тестів/проксі
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
		DBPath:     env("ODDINVEST_DB_PATH", "/var/lib/oddinvestd/oddinvest.db"),
		MQTTAddr:   env("ODDINVEST_MQTT_ADDR", ""),
		MQTTUser:   env("ODDINVEST_MQTT_USER", ""),
		MQTTPass:   env("ODDINVEST_MQTT_PASS", ""),
		MQTTPrefix: env("ODDINVEST_MQTT_PREFIX", "oddinvest"),
		NBUBase:    env("ODDINVEST_NBU_BASE", ""),
	}
}
