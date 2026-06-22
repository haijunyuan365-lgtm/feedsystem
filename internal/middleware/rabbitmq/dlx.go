package rabbitmq

import (
	"log"

	amqp "github.com/rabbitmq/amqp091-go"
)

const DLXExchange = "dlx.events"

func DeclareDLX(ch *amqp.Channel, queueName string) error {
	if ch == nil {
		return nil
	}
	if err := ch.ExchangeDeclare(DLXExchange, "topic", true, false, false, false, nil); err != nil {
		return err
	}
	dlxQueue := queueName + ".dlx"
	if _, err := ch.QueueDeclare(dlxQueue, true, false, false, false, nil); err != nil {
		return err
	}
	if err := ch.QueueBind(dlxQueue, "#", DLXExchange, false, nil); err != nil {
		return err
	}
	log.Printf("DLX ready: exchange=%s queue=%s", DLXExchange, dlxQueue)
	return nil
}
