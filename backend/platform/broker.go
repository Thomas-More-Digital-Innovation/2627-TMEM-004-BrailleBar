package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	Exchange     = "braillebar.events" // topic exchange, routing key = event type
	DeadExchange = "braillebar.dead"   // events that kept failing end up here, one queue per consumer
	maxAttempts  = 3
)

// Broker publishes events and runs consumers.
// When the connection is lost, Done closes and Consume and RunOutbox return an error. The service should then
// exit so Docker restarts it with a fresh connection (fail fast). Nothing is lost meanwhile: unacked events stay
// in RabbitMQ and unpublished ones stay in the outbox.
type Broker struct {
	conn *amqp.Connection
	pub  *amqp.Channel // publisher confirms are on; a channel is not safe for concurrent publishing
	mu   sync.Mutex
	done chan struct{}
}

// Connect retries until the broker is reachable or ctx ends, because services can start before RabbitMQ is ready.
func Connect(ctx context.Context, url string) (*Broker, error) {
	var conn *amqp.Connection
	for {
		var err error
		if conn, err = amqp.Dial(url); err == nil {
			break
		}
		slog.Warn("broker not ready", "error", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	pub, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, err
	}
	for _, ex := range []string{Exchange, DeadExchange} {
		if err = pub.ExchangeDeclare(ex, "topic", true, false, false, false, nil); err != nil {
			conn.Close()
			return nil, err
		}
	}
	if err = pub.Confirm(false); err != nil {
		conn.Close()
		return nil, err
	}

	b := &Broker{conn: conn, pub: pub, done: make(chan struct{})}
	connClosed := conn.NotifyClose(make(chan *amqp.Error, 1))
	pubClosed := pub.NotifyClose(make(chan *amqp.Error, 1))
	go func() {
		var reason *amqp.Error
		select {
		case reason = <-connClosed:
		case reason = <-pubClosed:
		}
		if reason != nil { // nil means we closed it ourselves
			slog.Error("broker connection lost", "error", reason)
		}
		close(b.done)
	}()
	return b, nil
}

func (b *Broker) Close() error { return b.conn.Close() }

// Done closes when the connection or the publishing channel is gone.
func (b *Broker) Done() <-chan struct{} { return b.done }

// Publish sends e right away and returns once the broker confirmed it stored the message.
// Services with a database should use Enqueue instead, so the event commits together with their own writes.
func (b *Broker) Publish(ctx context.Context, e Envelope) error {
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return b.publish(ctx, e.Type, e.ID, body)
}

func (b *Broker) publish(ctx context.Context, eventType, eventID string, body []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	conf, err := b.pub.PublishWithDeferredConfirmWithContext(ctx, Exchange, eventType, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		MessageId:    eventID,
		Body:         body,
	})
	if err != nil {
		return err
	}
	ok, err := conf.WaitContext(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("broker did not confirm the event")
	}
	return nil
}

type Handler func(ctx context.Context, e Envelope) error

// Consume binds a durable queue to the given event types and calls h for each event, one at a time.
// A failing handler is retried a few times; after that the event goes to "<queue>.dead" so it is kept, not lost.
// It blocks until ctx ends or the connection closes.
func (b *Broker) Consume(ctx context.Context, queue string, eventTypes []string, h Handler) error {
	ch, err := b.conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	dead := queue + ".dead"
	if _, err = ch.QueueDeclare(dead, true, false, false, false, nil); err != nil {
		return err
	}
	if err = ch.QueueBind(dead, queue, DeadExchange, false, nil); err != nil {
		return err
	}
	_, err = ch.QueueDeclare(queue, true, false, false, false, amqp.Table{
		"x-dead-letter-exchange":    DeadExchange,
		"x-dead-letter-routing-key": queue,
	})
	if err != nil {
		return err
	}
	for _, t := range eventTypes {
		if err = ch.QueueBind(queue, t, Exchange, false, nil); err != nil {
			return err
		}
	}
	return consume(ctx, ch, queue, h)
}

// Subscribe gives this instance its own temporary queue, so every replica gets every event; Consume shares one queue
// and each event goes to one replica. For pushes only: events that arrive while the instance is down are not kept.
func (b *Broker) Subscribe(ctx context.Context, eventTypes []string, h Handler) error {
	ch, err := b.conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	q, err := ch.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		return err
	}
	for _, t := range eventTypes {
		if err = ch.QueueBind(q.Name, t, Exchange, false, nil); err != nil {
			return err
		}
	}
	return consume(ctx, ch, q.Name, h)
}

func consume(ctx context.Context, ch *amqp.Channel, queue string, h Handler) error {
	if err := ch.Qos(1, 0, false); err != nil {
		return err
	}
	deliveries, err := ch.ConsumeWithContext(ctx, queue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}

	for d := range deliveries {
		var e Envelope
		if err := json.Unmarshal(d.Body, &e); err != nil || e.ID == "" {
			slog.Error("unreadable event, dead-lettering", "queue", queue)
			d.Nack(false, false)
			continue
		}
		if err := runWithRetry(ctx, h, e); err != nil {
			slog.Error("event failed, dead-lettering", "queue", queue, "eventId", e.ID, "type", e.Type, "error", err)
			d.Nack(false, false)
			continue
		}
		d.Ack(false)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("consumer %s stopped: connection closed", queue)
}

// Only the event id and type are logged, never the content (it can be a private document).
func runWithRetry(ctx context.Context, h Handler, e Envelope) error {
	var err error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err = h(ctx, e); err == nil {
			return nil
		}
		slog.Warn("handler failed", "eventId", e.ID, "type", e.Type, "attempt", attempt, "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
		}
	}
	return err
}
