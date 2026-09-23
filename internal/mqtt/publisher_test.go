package mqtt

import (
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

// Без брокера New не чекає й не падає, а публікатор лишається живим:
// документ запамʼятовано, і він піде з OnConnect, щойно брокер зʼявиться.
//
// Доти New чекав 15 с і повертав помилку, сервіс жив без публікатора до
// рестарту, а фоновий клієнт, підключившись, оголошував «online» над
// старим станом.
func TestPublisherWithoutBrokerKeepsLastDoc(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	start := time.Now()
	// Порт 1 на петлі — гарантовано ніхто не слухає.
	p := New("tcp://127.0.0.1:1", "", "", "test", "oddinvestd-test", log)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("New чекав %s — старт сервісу не має залежати від брокера", d)
	}
	defer p.c.Disconnect(0)

	doc := []byte(`{"schema":3}`)
	if err := p.PublishState(doc); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("без брокера: %v, чекали ErrNotConnected", err)
	}
	p.mu.Lock()
	last := string(p.last)
	p.mu.Unlock()
	if last != string(doc) {
		t.Errorf("останній документ %q — OnConnect не мав би що доштовхнути", last)
	}

	p.Retire()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.last != nil {
		t.Error("Retire лишив документ — перепідключення воскресило б видалений портфель")
	}
}
