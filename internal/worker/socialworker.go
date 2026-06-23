package worker

import (
	"context"
	"encoding/json"
	"errors"
	"feedsystem/internal/middleware/rabbitmq"
	"feedsystem/internal/social"
	"log"
	"time"

	"github.com/go-sql-driver/mysql"
	amqp "github.com/rabbitmq/amqp091-go"
)

type SocialWorker struct {
	ch    *amqp.Channel
	repo  *social.SocialRepository
	queue string
}

func NewSocialWorker(ch *amqp.Channel, repo *social.SocialRepository, queue string) *SocialWorker {
	return &SocialWorker{ch: ch, repo: repo, queue: queue}
}

func (w *SocialWorker) Run(ctx context.Context) error {
	if w == nil || w.ch == nil || w.repo == nil {
		return errors.New("social worker is not initialized")
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
			w.handleDelivery(ctx, d)
		}
	}
}

func (w *SocialWorker) handleDelivery(ctx context.Context, d amqp.Delivery) {
	const maxRetries = 3
	for i := 0; i <= maxRetries; i++ {
		select {
		case <-ctx.Done():
			_ = d.Nack(false, true)
			return
		default:
		}

		if err := w.process(ctx, d.Body); err != nil {
			if i >= maxRetries {
				log.Printf("social worker: 重试 %d 次后仍失败, 丢弃: %v", maxRetries, err)
				_ = d.Ack(false)
				return
			}
			wait := time.Duration(1<<uint(i)) * time.Second
			log.Printf("social worker: 处理失败, %v 后重试 (%d/%d): %v", wait, i+1, maxRetries, err)
			time.Sleep(wait)
			continue
		}
		_ = d.Ack(false)
		return
	}
}

func (w *SocialWorker) process(ctx context.Context, body []byte) error {
	var event rabbitmq.SocialEvent
	if err := json.Unmarshal(body, &event); err != nil {
		return nil
	}
	if event.FollowerID == 0 || event.VloggerID == 0 {
		return nil
	}

	switch event.Action {
	case "follow":
		err := w.repo.Follow(ctx, &social.Social{
			FollowerID: event.FollowerID,
			VloggerID:  event.VloggerID,
		})
		if err == nil {
			return nil
		}
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			return nil
		}
		return err
	case "unfollow":
		return w.repo.Unfollow(ctx, &social.Social{
			FollowerID: event.FollowerID,
			VloggerID:  event.VloggerID,
		})
	default:
		return nil
	}
}
