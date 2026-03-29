// Package queue manages the RabbitMQ connection, topology, publishing and
// consumption for VoiceLine's async audio processing pipeline.
//
// Topology:
//
//	voiceline.audio  (direct exchange)
//	  └── transcription.jobs      ← API publishes here after audio upload
//
//	voiceline.notes  (fanout exchange)
//	  └── integration.sheets      ← Sheets worker consumes here
//	  └── integration.hubspot     ← future integrations bind here
//
//	voiceline.dead   (direct exchange)
//	  └── *.failed queues         ← messages that exceeded max retries
package queue

import (
	"fmt"
	"log"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Exchange and queue name constants — single source of truth.
const (
	ExchangeAudio = "voiceline.audio"
	ExchangeNotes = "voiceline.notes"
	ExchangeDead  = "voiceline.dead"

	QueueTranscription      = "transcription.jobs"
	QueueIntegrationSheets  = "integration.sheets"
	QueueIntegrationHubspot = "integration.hubspot"
)

// Connection wraps an AMQP connection with automatic reconnection logic.
// RabbitMQ connections drop on network blips and broker restarts —
// this wrapper hides that complexity from callers.
type Connection struct {
	url  string
	conn *amqp.Connection
}

func NewConnection(url string) (*Connection, error) {
	c := &Connection{url: url}
	if err := c.dial(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Connection) dial() error {
	var err error
	for attempt := 1; attempt <= 5; attempt++ {
		c.conn, err = amqp.Dial(c.url)
		if err == nil {
			log.Println("RabbitMQ: connected")
			return nil
		}
		wait := time.Duration(attempt*attempt) * time.Second // 1s, 4s, 9s, 16s, 25s
		log.Printf("RabbitMQ: attempt %d/5 failed, retrying in %s: %v", attempt, wait, err)
		time.Sleep(wait)
	}
	return fmt.Errorf("RabbitMQ: could not connect after 5 attempts: %w", err)
}

// Channel opens a new AMQP channel.
func (c *Connection) Channel() (*amqp.Channel, error) {
	ch, err := c.conn.Channel()
	if err != nil {
		log.Printf("RabbitMQ: channel open failed, reconnecting: %v", err)
		if reconnErr := c.dial(); reconnErr != nil {
			return nil, fmt.Errorf("reconnect failed: %w", reconnErr)
		}
		return c.conn.Channel()
	}
	return ch, nil
}

// Close gracefully shuts down the AMQP connection.
func (c *Connection) Close() {
	if c.conn != nil && !c.conn.IsClosed() {
		c.conn.Close()
	}
}

func DeclareTopology(ch *amqp.Channel) error {
	// Exchanges
	for _, ex := range []struct{ name, kind string }{
		{ExchangeAudio, "direct"}, // routes by routing key
		{ExchangeNotes, "fanout"}, // broadcasts to all bound queues
		{ExchangeDead, "direct"},  // receives dead-lettered messages
	} {
		if err := ch.ExchangeDeclare(ex.name, ex.kind,
			true, false, false, false, nil,
		); err != nil {
			return fmt.Errorf("declaring exchange %q: %w", ex.name, err)
		}
	}

	// Queues with dead-letter config and TTL
	for _, q := range []struct {
		name, exchange, routingKey string
	}{
		{QueueTranscription, ExchangeAudio, "transcription"},
		{QueueIntegrationSheets, ExchangeNotes, ""},
		{QueueIntegrationHubspot, ExchangeNotes, ""},
	} {
		if _, err := ch.QueueDeclare(q.name,
			true, false, false, false,
			amqp.Table{
				// Failed messages are routed to the dead letter exchange automatically
				"x-dead-letter-exchange":    ExchangeDead,
				"x-dead-letter-routing-key": q.name + ".failed",
				// Messages expire after 24 hours if never consumed
				"x-message-ttl": int32(86_400_000),
			},
		); err != nil {
			return fmt.Errorf("declaring queue %q: %w", q.name, err)
		}

		if err := ch.QueueBind(q.name, q.routingKey, q.exchange, false, nil); err != nil {
			return fmt.Errorf("binding queue %q: %w", q.name, err)
		}
	}

	log.Println("RabbitMQ: topology declared")
	return nil
}
