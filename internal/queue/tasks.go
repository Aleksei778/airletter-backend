// Package queue defines asynq task types shared by the API and the worker.
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

const (
	TypeSendEmail = "email:send"
	TypeDispatch  = "campaign:dispatch"

	QueueSend    = "send"
	QueueDefault = "default"
)

type SendEmailPayload struct {
	RecipientID uint `json:"recipient_id"`
}

// NewSendEmailTask creates a task sending one email. The task ID is derived from
// the recipient, so the same recipient cannot be queued twice at the same time.
func NewSendEmailTask(recipientID uint, maxRetry int, delay time.Duration) (*asynq.Task, error) {
	payload, err := json.Marshal(SendEmailPayload{RecipientID: recipientID})
	if err != nil {
		return nil, err
	}

	return asynq.NewTask(TypeSendEmail, payload,
		asynq.TaskID(fmt.Sprintf("send:%d", recipientID)),
		asynq.Queue(QueueSend),
		asynq.MaxRetry(maxRetry),
		asynq.Timeout(2*time.Minute),
		asynq.ProcessIn(delay),
	), nil
}

// NewDispatchTask creates the task releasing pending recipients into the queue.
// Unique keeps at most one dispatch waiting even with several schedulers.
func NewDispatchTask(uniqueFor time.Duration) *asynq.Task {
	return asynq.NewTask(TypeDispatch, nil,
		asynq.Queue(QueueDefault),
		asynq.MaxRetry(0),
		asynq.Unique(uniqueFor),
	)
}

// Trigger enqueues an immediate dispatch, e.g. right after a campaign is created
type Trigger struct {
	client    *asynq.Client
	uniqueFor time.Duration
}

func NewTrigger(client *asynq.Client, uniqueFor time.Duration) *Trigger {
	return &Trigger{client: client, uniqueFor: uniqueFor}
}

func (t *Trigger) TriggerDispatch(ctx context.Context) error {
	_, err := t.client.EnqueueContext(ctx, NewDispatchTask(t.uniqueFor))
	if errors.Is(err, asynq.ErrDuplicateTask) {
		return nil // a dispatch is already waiting
	}
	return err
}
