package queue

import (
	"context"
	"encoding/json"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"

	"github-release-notifier/internal/domain"
)

type publisher interface {
	PublishWithContext(ctx context.Context, exchange, key string, mandatory, immediate bool, msg amqp.Publishing) error
}

type NotificationQueue struct {
	pub publisher
}

func NewNotificationQueue(pub publisher) *NotificationQueue {
	return &NotificationQueue{pub: pub}
}

func (q *NotificationQueue) EnqueueBatch(ctx context.Context, jobs []domain.NotificationJob) error {
	for _, job := range jobs {
		data, err := json.Marshal(job)
		if err != nil {
			return fmt.Errorf("marshalling job: %w", err)
		}
		if err := q.pub.PublishWithContext(ctx, "", notificationsQueue, false, false, amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         data,
		}); err != nil {
			return fmt.Errorf("publishing job: %w", err)
		}
	}
	return nil
}
