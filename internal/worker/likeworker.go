package worker

import (
	"context"
	"encoding/json"
	"errors"
	"feedsystem/internal/middleware/rabbitmq"
	"log"

	amqp "github.com/rabbitmq/amqp091-go"
)

type LikeWorker struct {
	ch    *amqp.Channel
	queue string
}

func NewLikeWorker(ch *amqp.Channel, queue string) *LikeWorker {
	return &LikeWorker{ch: ch, queue: queue}
}

func (w *LikeWorker) Run(ctx context.Context) error {
	if w == nil || w.ch == nil {
		return errors.New("like worker is not initialized")
	}
	if w.queue == "" {
		return errors.New("queue is required")
	}

	deliveries, err := w.ch.Consume(w.queue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d, ok := <-deliveries:
			if !ok {
				return errors.New("deliveries channel closed")
			}
			w.handleDelivery(d)
		}
	}
}

func (w *LikeWorker) handleDelivery(d amqp.Delivery) {
	var event rabbitmq.LikeEvent
	if err := json.Unmarshal(d.Body, &event); err != nil {
		log.Printf("like worker: invalid event, discard: %v", err)
		_ = d.Nack(false, false)
		return
	}
	if event.UserID == 0 || event.VideoID == 0 {
		log.Printf("like worker: invalid event fields, discard: event_id=%s", event.EventID)
		_ = d.Nack(false, false)
		return
	}

	log.Printf(
		"like worker: event_id=%s action=%s user_id=%d video_id=%d occurred_at=%s",
		event.EventID,
		event.Action,
		event.UserID,
		event.VideoID,
		event.OccurredAt.Format("2006-01-02 15:04:05"),
	)
	_ = d.Ack(false)
}
