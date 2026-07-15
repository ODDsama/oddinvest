// Package mqtt — публікація стану в брокер для інтеграції HA.
// Контракт: retained {prefix}/state (JSON за contract/oddinvest-state.schema.json),
// LWT {prefix}/availability = online/offline.
package mqtt

import (
	"fmt"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

type Publisher struct {
	c      paho.Client
	prefix string
}

// New під'єднується до брокера з LWT. addr — tcp://host:1883.
func New(addr, user, pass, prefix, clientID string) (*Publisher, error) {
	opts := paho.NewClientOptions().
		AddBroker(addr).
		SetClientID(clientID).
		SetUsername(user).
		SetPassword(pass).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5*time.Second).
		SetWill(prefix+"/availability", "offline", 1, true)
	opts.OnConnect = func(c paho.Client) {
		c.Publish(prefix+"/availability", 1, true, "online")
	}
	c := paho.NewClient(opts)
	tok := c.Connect()
	if !tok.WaitTimeout(15 * time.Second) {
		return nil, fmt.Errorf("mqtt: таймаут підключення до %s", addr)
	}
	if err := tok.Error(); err != nil {
		return nil, fmt.Errorf("mqtt: %w", err)
	}
	return &Publisher{c: c, prefix: prefix}, nil
}

// PublishState публікує документ стану (retained, QoS1).
func (p *Publisher) PublishState(doc []byte) error {
	tok := p.c.Publish(p.prefix+"/state", 1, true, doc)
	if !tok.WaitTimeout(10 * time.Second) {
		return fmt.Errorf("mqtt: таймаут публікації state")
	}
	return tok.Error()
}

func (p *Publisher) Close() {
	p.c.Publish(p.prefix+"/availability", 1, true, "offline").WaitTimeout(3 * time.Second)
	p.c.Disconnect(250)
}
