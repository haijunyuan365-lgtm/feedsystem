package main

import (
	"context"
	"feedsystem/internal/config"
	"feedsystem/internal/db"
	"feedsystem/internal/middleware/rabbitmq"
	rediscache "feedsystem/internal/middleware/redis"
	"feedsystem/internal/social"
	"feedsystem/internal/video"
	"feedsystem/internal/worker"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	socialExchange   = "social.events"
	socialQueue      = "social.events"
	socialBindingKey = "social.*"

	likeExchange   = "like.events"
	likeQueue      = "like.events"
	likeBindingKey = "like.*"

	commentExchange   = "comment.events"
	commentQueue      = "comment.events"
	commentBindingKey = "comment.*"

	popularityExchange   = "video.popularity.events"
	popularityQueue      = "video.popularity.events"
	popularityBindingKey = "video.popularity.*"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println(".env not found; continuing")
	}

	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "configs/config.yaml"
	}
	cfg, _, err := config.LoadLocalDev(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	//连接mysql
	sqlDB, err := db.NewDB(cfg.Database)
	if err != nil {
		log.Fatalf("Failed to connect MySQL: %v", err)
	}
	defer db.CloseDB(sqlDB)

	// 连接 Redis (可选，用于缓存)
	cache, err := rediscache.NewFromEnv(&cfg.Redis)
	if err != nil {
		log.Printf("Redis config error (cache disabled): %v", err)
		cache = nil
	}
	defer cache.Close()

	//连接rabbitMQ
	rmq, err := rabbitmq.NewRabbitMQ(&cfg.RabbitMQ)
	if err != nil {
		log.Fatalf("Failed to connect RabbitMQ: %v", err)
	}
	defer rmq.Close()

	topologyCh, err := rmq.NewChannel()
	if err != nil {
		log.Fatalf("Failed to open topology channel: %v", err)
	}
	if err := rabbitmq.DeclareTopic(topologyCh, socialExchange, socialQueue, socialBindingKey); err != nil {
		log.Fatalf("Failed to declare social topology: %v", err)
	}
	if err := rabbitmq.DeclareTopic(topologyCh, likeExchange, likeQueue, likeBindingKey); err != nil {
		log.Fatalf("Failed to declare like topology: %v", err)
	}
	if err := rabbitmq.DeclareTopic(topologyCh, commentExchange, commentQueue, commentBindingKey); err != nil {
		log.Fatalf("Failed to declare comment topology: %v", err)
	}
	if err := rabbitmq.DeclareTopic(topologyCh, popularityExchange, popularityQueue, popularityBindingKey); err != nil {
		log.Fatalf("Failed to declare popularity topology: %v", err)
	}
	_ = topologyCh.Close()

	workerCh, err := rmq.NewChannel()
	if err != nil {
		log.Fatalf("Failed to open worker channel: %v", err)
	}
	defer workerCh.Close()
	if err := workerCh.Qos(50, 0, false); err != nil {
		log.Printf("LikeWorker QoS setup failed: %v", err)
	}

	socialRepo := social.NewSocialRepository(sqlDB)
	likeRepo := video.NewLikeRepository(sqlDB)
	videoRepo := video.NewVideoRepository(sqlDB)
	commentRepo := video.NewCommentRepository(sqlDB)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go runWorkerWithRetry(ctx, "SocialWorker", rmq.Conn, func(ch *amqp.Channel) error {
		return worker.NewSocialWorker(ch, socialRepo, socialQueue).Run(ctx)
	})
	go runWorkerWithRetry(ctx, "LikeWorker", rmq.Conn, func(ch *amqp.Channel) error {
		return worker.NewLikeWorker(ch, likeRepo, videoRepo, likeQueue).Run(ctx)
	})
	go runWorkerWithRetry(ctx, "CommentWorker", rmq.Conn, func(ch *amqp.Channel) error {
		return worker.NewCommentWorker(ch, commentRepo, videoRepo, commentQueue).Run(ctx)
	})
	go runWorkerWithRetry(ctx, "PopularityWorker", rmq.Conn, func(ch *amqp.Channel) error {
		return worker.NewPopularityWorker(ch, cache, popularityQueue).Run(ctx)
	})

	<-ctx.Done()
	log.Printf("Worker shutting down...")
	time.Sleep(2 * time.Second)
	log.Printf("Worker stopped")

}
func runWorkerWithRetry(ctx context.Context, name string, conn *amqp.Connection, fn func(*amqp.Channel) error) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		ch, err := conn.Channel()
		if err != nil {
			log.Printf("%s: 创建 Channel 失败: %v, 5秒后重试", name, err)
			time.Sleep(5 * time.Second)
			continue
		}
		if err := ch.Qos(50, 0, false); err != nil {
			log.Printf("%s: QoS 设置失败: %v", name, err)
		}

		log.Printf("%s started, consuming", name)
		if err := fn(ch); err != nil {
			if ctx.Err() != nil {
				_ = ch.Close()
				return
			}
			log.Printf("%s: %v, 5秒后重连...", name, err)
		}
		_ = ch.Close()
		time.Sleep(5 * time.Second)
	}
}
