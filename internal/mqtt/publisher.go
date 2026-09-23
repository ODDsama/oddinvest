// Package mqtt — публікація стану в брокер для інтеграції HA.
// Контракт: retained {prefix}/state (JSON за contract/oddinvest-state.schema.json),
// LWT {prefix}/availability = online/offline.
package mqtt

import (
	"errors"
	"log/slog"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// ErrNotConnected — брокера зараз немає. Не поломка публікації: документ
// запамʼятовано, і він піде сам, щойно зʼєднання підніметься (OnConnect).
var ErrNotConnected = errors.New("mqtt: брокер недоступний — стан піде, щойно зʼєднання підніметься")

type Publisher struct {
	c      paho.Client
	prefix string

	mu   sync.Mutex
	last []byte // останній документ стану — для доштовхування після (пере)підключення
}

// New — публікатор із LWT, що підключається У ФОНІ. addr — tcp://host:1883.
//
// ЧОМУ НЕ ЧЕКАЄ НА БРОКЕР. Доти New чекав підключення 15 с і на таймауті
// повертав помилку — а клієнт із SetConnectRetry тим часом лишався живим і
// підключався пізніше сам. Сервіс же вважав, що публікатора немає (nil), і
// більше ніколи нічого не публікував; а клієнт, підключившись, оголошував
// retained availability=online. Після перезавантаження хоста, коли
// контейнер сервісу стартує раніше за брокер, Home Assistant показував
// старий стан як живий аж до ручного рестарту.
//
// Тепер публікатор є завжди, а підключення — його внутрішня справа:
// PublishState запамʼятовує документ і, якщо брокера немає, каже про це
// ErrNotConnected; OnConnect після кожного (пере)підключення доштовхує
// останній документ. Жоден стан не губиться між «брокер упав» і «брокер
// піднявся».
func New(addr, user, pass, prefix, clientID string, log *slog.Logger) *Publisher {
	p := &Publisher{prefix: prefix}
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
		// Не чекати токени тут: колбек іде з горутини клієнта, і
		// блокування в ньому затримало б саму обробку зʼєднання.
		c.Publish(prefix+"/availability", 1, true, "online")
		p.mu.Lock()
		last := p.last
		p.mu.Unlock()
		if last != nil {
			c.Publish(prefix+"/state", 1, true, last)
		}
		log.Info("mqtt: підключено", "broker", addr, "prefix", prefix)
	}
	opts.OnConnectionLost = func(_ paho.Client, err error) {
		log.Warn("mqtt: зʼєднання втрачено — перепідключаюсь", "prefix", prefix, "err", err)
	}
	p.c = paho.NewClient(opts)
	tok := p.c.Connect()
	go func() {
		// З SetConnectRetry токен завершується лише успіхом або Disconnect;
		// помилка тут — рідкість (кривий addr), але мовчати про неї не можна.
		tok.Wait()
		if err := tok.Error(); err != nil {
			log.Error("mqtt: підключення", "broker", addr, "err", err)
		}
	}()
	return p
}

// PublishState публікує документ стану (retained, QoS1). Без брокера —
// запамʼятовує й повертає ErrNotConnected: документ піде з OnConnect.
func (p *Publisher) PublishState(doc []byte) error {
	p.mu.Lock()
	p.last = doc
	p.mu.Unlock()
	if !p.c.IsConnectionOpen() {
		return ErrNotConnected
	}
	tok := p.c.Publish(p.prefix+"/state", 1, true, doc)
	if !tok.WaitTimeout(10 * time.Second) {
		return errors.New("mqtt: таймаут публікації state")
	}
	return tok.Error()
}

func (p *Publisher) Close() {
	p.c.Publish(p.prefix+"/availability", 1, true, "offline").WaitTimeout(3 * time.Second)
	p.c.Disconnect(250)
}

// Retire — публікатор портфеля, якого більше НЕМАЄ: те саме, що Close,
// плюс стерти retained-стан. Close лишає останній документ на брокері
// навмисно (рестарт сервісу не має гасити сенсори до першої публікації),
// а для видаленого портфеля це привид: Home Assistant при кожному
// перепідключенні отримував би знову стан, якого вже не існує. Порожній
// retained-payload — спосіб MQTT стерти повідомлення з топіка.
func (p *Publisher) Retire() {
	p.mu.Lock()
	p.last = nil // інакше OnConnect воскресив би стан видаленого портфеля
	p.mu.Unlock()
	p.c.Publish(p.prefix+"/state", 1, true, []byte{}).WaitTimeout(3 * time.Second)
	p.Close()
}
