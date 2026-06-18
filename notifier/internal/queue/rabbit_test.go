package queue

import (
	"context"
	"encoding/json"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"

	"github-release-notifier/notifier/internal/domain"
)

// fakeAck stands in for the broker's acknowledger so Ack/Nack can be asserted
// without a live channel.
type fakeAck struct {
	acked   bool
	nacked  bool
	requeue bool
}

func (f *fakeAck) Ack(uint64, bool) error { f.acked = true; return nil }
func (f *fakeAck) Nack(_ uint64, _, requeue bool) error {
	f.nacked = true
	f.requeue = requeue
	return nil
}
func (f *fakeAck) Reject(uint64, bool) error { return nil }

func deliveryFor(t *testing.T, job domain.NotificationJob, headers amqp.Table, ack amqp.Acknowledger) amqp.Delivery {
	t.Helper()
	body, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("marshal job: %v", err)
	}
	return amqp.Delivery{Acknowledger: ack, Headers: headers, Body: body}
}

func sampleJob() domain.NotificationJob {
	return domain.NotificationJob{SubscriptionID: 1, Email: "u@example.com", Repo: "x/y", Tag: "v1", UnsubToken: "t"}
}

func TestDequeue_ParsesJobAndDeliveryCount(t *testing.T) {
	ch := make(chan amqp.Delivery, 1)
	c := &Consumer{deliveries: ch}
	job := sampleJob()
	ch <- deliveryFor(t, job, amqp.Table{"x-delivery-count": int64(3)}, &fakeAck{})

	d, err := c.Dequeue(context.Background())
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if d.Job != job {
		t.Fatalf("job = %+v, want %+v", d.Job, job)
	}
	if d.DeliveryCount != 3 {
		t.Fatalf("DeliveryCount = %d, want 3", d.DeliveryCount)
	}
}

func TestDequeue_FirstDeliveryHasZeroCount(t *testing.T) {
	ch := make(chan amqp.Delivery, 1)
	c := &Consumer{deliveries: ch}
	ch <- deliveryFor(t, sampleJob(), nil, &fakeAck{})

	d, err := c.Dequeue(context.Background())
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if d.DeliveryCount != 0 {
		t.Fatalf("DeliveryCount = %d, want 0 when header absent", d.DeliveryCount)
	}
}

func TestDequeue_PoisonMessageIsDroppedNotRedelivered(t *testing.T) {
	ch := make(chan amqp.Delivery, 1)
	c := &Consumer{deliveries: ch}
	ack := &fakeAck{}
	ch <- amqp.Delivery{Acknowledger: ack, Body: []byte("{not json")}

	if _, err := c.Dequeue(context.Background()); err == nil {
		t.Fatal("expected an error for an unparseable message")
	}
	if !ack.acked {
		t.Fatal("poison message must be acked (dropped), else it redelivers forever")
	}
}

func TestDequeue_ClosedChannelReturnsError(t *testing.T) {
	ch := make(chan amqp.Delivery)
	close(ch)
	c := &Consumer{deliveries: ch}

	if _, err := c.Dequeue(context.Background()); err == nil {
		t.Fatal("expected an error when the consumer channel is closed")
	}
}

func TestDequeue_CancelledContextReturnsError(t *testing.T) {
	c := &Consumer{deliveries: make(chan amqp.Delivery)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := c.Dequeue(ctx); err == nil {
		t.Fatal("expected ctx error on a cancelled context")
	}
}

func TestAck_AcknowledgesDelivery(t *testing.T) {
	ack := &fakeAck{}
	c := &Consumer{}
	if err := c.Ack(&Delivery{raw: amqp.Delivery{Acknowledger: ack}}); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if !ack.acked {
		t.Fatal("expected the delivery to be acked")
	}
}

func TestNack_RequeuesDelivery(t *testing.T) {
	ack := &fakeAck{}
	c := &Consumer{}
	if err := c.Nack(&Delivery{raw: amqp.Delivery{Acknowledger: ack}}); err != nil {
		t.Fatalf("Nack: %v", err)
	}
	if !ack.nacked || !ack.requeue {
		t.Fatalf("expected nack with requeue=true, got nacked=%v requeue=%v", ack.nacked, ack.requeue)
	}
}
