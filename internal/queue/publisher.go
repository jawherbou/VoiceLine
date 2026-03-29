// This file defines the message schemas shared between the API layer and
// the workers, and the Publisher that enqueues them.
package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// AudioJob is published by the API handler right after audio is received.
// The transcription worker downloads the audio, calls Whisper, then publishes
// a NoteJob downstream.
type AudioJob struct {
	JobID     string `json:"job_id"`
	UserID    string `json:"user_id"`
	AudioPath string `json:"audio_path"` // temp file path on disk
	Filename  string `json:"filename"`   // original name — used to detect codec
	QueuedAt  string `json:"queued_at"`  // RFC3339 UTC
}

// NoteJob is published by the transcription worker after a successful Whisper call.
// It fans out via ExchangeNotes to all integration workers simultaneously.
type NoteJob struct {
	JobID      string `json:"job_id"`
	UserID     string `json:"user_id"`
	Transcript string `json:"transcript"`
	QueuedAt   string `json:"queued_at"`
}

type Publisher struct {
	ch *amqp.Channel
}

// NewPublisher opens a channel from the connection and wraps it.
func NewPublisher(conn *Connection) (*Publisher, error) {
	ch, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("opening publisher channel: %w", err)
	}
	return &Publisher{ch: ch}, nil
}

func (p *Publisher) PublishAudioJob(ctx context.Context, job AudioJob) error {
	job.QueuedAt = time.Now().UTC().Format(time.RFC3339)

	body, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshalling AudioJob: %w", err)
	}

	return p.ch.PublishWithContext(ctx,
		ExchangeAudio,   // exchange
		"transcription", // routing key — matches QueueTranscription binding
		true,            // mandatory — error if no queue accepts this message
		false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent, // survives broker restart
			MessageId:    job.JobID,       // visible in RabbitMQ management UI
			Timestamp:    time.Now(),
			Body:         body,
		},
	)
}

func (p *Publisher) PublishNoteJob(ctx context.Context, job NoteJob) error {
	job.QueuedAt = time.Now().UTC().Format(time.RFC3339)

	body, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshalling NoteJob: %w", err)
	}

	return p.ch.PublishWithContext(ctx,
		ExchangeNotes, // fanout — routing key is ignored
		"",
		true,
		false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			MessageId:    job.JobID,
			Timestamp:    time.Now(),
			Body:         body,
		},
	)
}

// Close shuts down the underlying AMQP channel.
func (p *Publisher) Close() {
	if p.ch != nil {
		p.ch.Close()
	}
}
