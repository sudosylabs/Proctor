// Package memory provides an in-process sender for tests and local
// development.
package memory

import (
	"context"
	"sync"

	"github.com/sudosylabs/proctor/packages/mail"
)

// Sender composes and records accepted deliveries in memory.
type Sender struct {
	mu         sync.RWMutex
	composer   *mail.Composer
	deliveries []mail.Delivery
}

// New constructs an empty in-memory sender.
func New(config mail.ComposerConfig) (*Sender, error) {
	composer, err := mail.NewComposer(config)
	if err != nil {
		return nil, err
	}
	return &Sender{composer: composer}, nil
}

func (s *Sender) Capabilities() mail.Capabilities {
	return s.composer.Capabilities()
}

func (s *Sender) Send(ctx context.Context, message mail.Message) (mail.Receipt, error) {
	if err := ctx.Err(); err != nil {
		return mail.Receipt{}, mail.Error("send", err)
	}
	delivery, err := s.composer.Compose(message)
	if err != nil {
		return mail.Receipt{}, err
	}
	if err := ctx.Err(); err != nil {
		return mail.Receipt{}, mail.Error("send", err)
	}

	s.mu.Lock()
	s.deliveries = append(s.deliveries, cloneDelivery(delivery))
	s.mu.Unlock()
	return mail.Receipt{
		MessageID:  delivery.MessageID,
		Recipients: append([]string(nil), delivery.Recipients...),
	}, nil
}

// Deliveries returns a deep snapshot ordered by acceptance time.
func (s *Sender) Deliveries() []mail.Delivery {
	s.mu.RLock()
	defer s.mu.RUnlock()

	deliveries := make([]mail.Delivery, len(s.deliveries))
	for i, delivery := range s.deliveries {
		deliveries[i] = cloneDelivery(delivery)
	}
	return deliveries
}

// Reset removes all retained deliveries.
func (s *Sender) Reset() {
	s.mu.Lock()
	s.deliveries = nil
	s.mu.Unlock()
}

func cloneDelivery(delivery mail.Delivery) mail.Delivery {
	delivery.Recipients = append([]string(nil), delivery.Recipients...)
	delivery.Data = append([]byte(nil), delivery.Data...)
	return delivery
}

var _ mail.Sender = (*Sender)(nil)
