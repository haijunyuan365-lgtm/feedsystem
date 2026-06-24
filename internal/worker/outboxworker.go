package worker

import (
	"context"
	"encoding/json"
	"feedsystem/internal/middleware/rabbitmq"
	rediscache "feedsystem/internal/middleware/redis"
	"feedsystem/internal/video"
	"fmt"
	"log"
	"time"

	oredis "github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

func StartOutboxPoller(db *gorm.DB, tmq *rabbitmq.TimelineMQ) {
	if db == nil || tmq == nil {
		log.Printf("Outbox poller disabled: timeline mq is not initialized")
		return
	}

	go func() {
		for {
			var messages []video.OutboxMsg
			err := db.Where("status = ?", "pending").Order("create_time ASC").Limit(100).Find(&messages).Error
			if err != nil || len(messages) == 0 {
				time.Sleep(1 * time.Second)
				continue
			}

			for _, msg := range messages {
				err := tmq.PublishVideo(context.Background(), msg.VideoID, msg.CreateTime)
				if err == nil {
					if err := db.Delete(&msg).Error; err != nil {
						log.Printf("删除 outbox 消息失败: id=%d, err=%v", msg.ID, err)
					}
				} else {
					log.Printf("投递MQ失败: VideoID: %d, err: %v", msg.VideoID, err)
				}
			}
		}
	}()
}

func StartConsumer(tmq *rabbitmq.TimelineMQ, queueName string, redisClient *rediscache.Client, rmq *rabbitmq.RabbitMQ) {
	if tmq == nil || rmq == nil || rmq.Conn == nil {
		log.Printf("Timeline consumer disabled: rabbitmq is not initialized")
		return
	}
	if redisClient == nil {
		log.Printf("Timeline consumer disabled: redis is not initialized")
		return
	}

	go func() {
		for {
			ch, err := rmq.NewChannel()
			if err != nil {
				log.Printf("Timeline consumer: 创建 Channel 失败: %v, 5秒后重试", err)
				time.Sleep(5 * time.Second)
				continue
			}
			//防止 RabbitMQ 一口气把大量消息都推给这个 consumer，导致内存压力或某个 consumer 被塞爆。
			if err := ch.Qos(10, 0, false); err != nil {
				log.Printf("Timeline consumer: QoS 设置失败: %v", err)
			}

			msgs, err := ch.Consume(queueName, "", false, false, false, false, nil)
			if err != nil {
				log.Printf("Timeline consumer: 注册消费失败: %v, 5秒后重试", err)
				ch.Close()
				time.Sleep(5 * time.Second)
				continue
			}

			log.Printf("Timeline consumer 已启动, queue=%s", queueName)
			//队列暂时没消息：for msg := range msgs 会等着，不会退出
			//消费通道异常断开：msgs 被关闭，for range 才会退出,就会启动重连策略
			for msg := range msgs {
				var event rabbitmq.TimelineEvent
				if err := json.Unmarshal(msg.Body, &event); err != nil {
					log.Printf("Timeline consumer: 反序列化失败: %v", err)
					msg.Ack(false)
					continue
				}

				ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
				timelineKey := redisClient.Key("feed:global_timeline")
				err = redisClient.ZAdd(ctx, timelineKey, oredis.Z{
					Score:  float64(event.CreateTime),
					Member: fmt.Sprintf("%d", event.VideoID),
				})
				if err != nil {
					log.Printf("Timeline consumer: 写入Zset失败: %v", err)
					msg.Nack(false, true)
					cancel()
					continue
				}

				if err := redisClient.ZRemRangeByRank(ctx, timelineKey, 0, -1001); err != nil {
					log.Printf("Timeline consumer: ZRem失败: %v", err)
				}

				msg.Ack(false)
				cancel()
			}

			ch.Close()
			log.Printf("Timeline consumer: Channel 断开, 5秒后重连...")
			time.Sleep(5 * time.Second)
		}
	}()
}
