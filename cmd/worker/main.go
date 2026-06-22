package main

import (
	"context"
	"feedsystem/internal/config"
	"feedsystem/internal/middleware/rabbitmq"
	"feedsystem/internal/worker"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
)

const (
	likeExchange   = "like.events"
	likeQueue      = "like.events"
	likeBindingKey = "like.*"
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

	rmq, err := rabbitmq.NewRabbitMQ(&cfg.RabbitMQ)
	if err != nil {
		log.Fatalf("Failed to connect RabbitMQ: %v", err)
	}
	defer rmq.Close()

	topologyCh, err := rmq.NewChannel()
	if err != nil {
		log.Fatalf("Failed to open topology channel: %v", err)
	}
	if err := rabbitmq.DeclareTopic(topologyCh, likeExchange, likeQueue, likeBindingKey); err != nil {
		log.Fatalf("Failed to declare like topology: %v", err)
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("LikeWorker started, consuming queue=%s", likeQueue)
	if err := worker.NewLikeWorker(workerCh, likeQueue).Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("LikeWorker stopped: %v", err)
	}
}
