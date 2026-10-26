// Package sim reads the Simulator's clock: a one-shot fetch for the initial run
// and time, and a NATS tick subscription that streams the simulated clock.
package sim

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/nats-io/nats.go"
)

// Clock is the subset of the Simulator's clock the dispatcher needs.
type Clock struct {
	RunID       string    `json:"run_id"`
	SimulatedAt time.Time `json:"simulated_at"`
	Paused      bool      `json:"paused"`
}

// FetchClock reads the Simulator's current clock over HTTP.
func FetchClock(ctx context.Context, baseURL string) (Clock, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/clock", nil)
	if err != nil {
		return Clock{}, err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return Clock{}, err
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Clock{}, fmt.Errorf("simulator /clock returned %d", resp.StatusCode)
	}
	var c Clock
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		return Clock{}, err
	}
	return c, nil
}

// Subscriber streams Simulator ticks from NATS and invokes a callback per tick.
type Subscriber struct {
	conn *nats.Conn
	sub  *nats.Subscription
}

type tickMessage struct {
	RunID       string    `json:"run_id"`
	SimulatedAt time.Time `json:"simulated_at"`
	Paused      bool      `json:"paused"`
}

// Subscribe connects to NATS and invokes onTick for every tick. onTick is called
// serially (NATS dispatches a subscription's messages on one goroutine).
func Subscribe(url, subject string, log *slog.Logger, onTick func(Clock)) (*Subscriber, error) {
	conn, err := nats.Connect(url,
		nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) { log.Warn("nats disconnected", "error", err) }),
		nats.ReconnectHandler(func(c *nats.Conn) { log.Info("nats reconnected", "url", c.ConnectedUrl()) }),
	)
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}
	sub, err := conn.Subscribe(subject, func(msg *nats.Msg) {
		var t tickMessage
		if err := json.Unmarshal(msg.Data, &t); err != nil {
			log.Warn("bad tick message", "error", err)
			return
		}
		onTick(Clock{RunID: t.RunID, SimulatedAt: t.SimulatedAt, Paused: t.Paused})
	})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("subscribe %q: %w", subject, err)
	}
	log.Info("subscribed to ticks", "subject", subject, "url", conn.ConnectedUrl())
	return &Subscriber{conn: conn, sub: sub}, nil
}

// Close unsubscribes and drains the connection.
func (s *Subscriber) Close() {
	if s.sub != nil {
		_ = s.sub.Unsubscribe()
	}
	if s.conn != nil {
		_ = s.conn.Drain()
	}
}
