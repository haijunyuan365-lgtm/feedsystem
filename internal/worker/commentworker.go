package worker

import (
	"context"
	"encoding/json"
	"errors"
	"feedsystem/internal/middleware/rabbitmq"
	"feedsystem/internal/video"
	"log"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type CommentWorker struct {
	ch       *amqp.Channel
	comments *video.CommentRepository
	videos   *video.VideoRepository
	queue    string
}

func NewCommentWorker(ch *amqp.Channel, comments *video.CommentRepository, videos *video.VideoRepository, queue string) *CommentWorker {
	return &CommentWorker{ch: ch, comments: comments, videos: videos, queue: queue}
}

func (w *CommentWorker) Run(ctx context.Context) error {
	if w == nil || w.ch == nil || w.comments == nil || w.videos == nil {
		return errors.New("comment worker is not initialized")
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

func (w *CommentWorker) handleDelivery(ctx context.Context, d amqp.Delivery) {
	const maxRetries = 3
	for i := 0; i <= maxRetries; i++ {
		select {
		case <-ctx.Done():
			//消息处理失败，将单条消息重新入队。如果第二个参数是false，那么就会看是否有DLX，如果有就会进入DLX，没有就丢弃
			_ = d.Nack(false, true)
			return
		default:
		}

		if err := w.process(ctx, d.Body); err != nil {
			if i >= maxRetries {
				log.Printf("comment worker: 重试 %d 次后仍失败, 丢弃: %v", maxRetries, err)
				_ = d.Ack(false)
				return
			}
			wait := time.Duration(1<<uint(i)) * time.Second
			log.Printf("comment worker: 处理失败, %v 后重试 (%d/%d): %v", wait, i+1, maxRetries, err)
			time.Sleep(wait)
			continue
		}
		_ = d.Ack(false)
		return
	}
}

func (w *CommentWorker) process(ctx context.Context, body []byte) error {
	var event rabbitmq.CommentEvent
	if err := json.Unmarshal(body, &event); err != nil {
		return nil
	}

	switch event.Action {
	case "publish":
		return w.applyPublish(ctx, &event)
	case "delete":
		return w.applyDelete(ctx, &event)
	default:
		return nil
	}
}

func (w *CommentWorker) applyPublish(ctx context.Context, event *rabbitmq.CommentEvent) error {
	if event == nil || event.VideoID == 0 || event.AuthorID == 0 || strings.TrimSpace(event.Content) == "" {
		return nil
	}

	ok, err := w.videos.IsExist(ctx, event.VideoID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	comment := &video.Comment{
		Username: strings.TrimSpace(event.Username),
		VideoID:  event.VideoID,
		AuthorID: event.AuthorID,
		Content:  strings.TrimSpace(event.Content),
	}
	if err := w.comments.CreateComment(ctx, comment); err != nil {
		return err
	}
	return w.videos.ChangePopularity(ctx, event.VideoID, 1)
}

func (w *CommentWorker) applyDelete(ctx context.Context, event *rabbitmq.CommentEvent) error {
	if event == nil || event.CommentID == 0 {
		return nil
	}
	comment, err := w.comments.GetByID(ctx, event.CommentID)
	if err != nil {
		return err
	}
	if comment == nil {
		return nil
	}
	return w.comments.DeleteComment(ctx, comment)
}
