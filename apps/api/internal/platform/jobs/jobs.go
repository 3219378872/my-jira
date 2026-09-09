package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"my-jira/apps/api/internal/platform/database"
)

type Publisher interface {
	Publish(context.Context, database.DBTX, string, any, string) error
}
type Outbox struct{}

func (Outbox) Publish(ctx context.Context, q database.DBTX, topic string, payload any, key string) error {
	if topic == "" || key == "" {
		return fmt.Errorf("outbox topic and deduplication key are required")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO outbox_events(id,topic,payload,deduplication_key,created_at,updated_at)
		VALUES($1,$2,$3::jsonb,$4,now(),now()) ON CONFLICT(deduplication_key) DO NOTHING`, uuid.New(), topic, string(data), key)
	return err
}

type Envelope struct {
	EventID uuid.UUID       `json:"event_id"`
	Payload json.RawMessage `json:"payload"`
}

func Dispatch(ctx context.Context, db *database.Database, client *asynq.Client) error {
	return db.WithinTx(ctx, func(q database.DBTX) error {
		rows, err := q.QueryContext(ctx, `SELECT id,topic,payload FROM outbox_events WHERE dispatched_at IS NULL AND deleted_at IS NULL
			ORDER BY created_at LIMIT 50 FOR UPDATE SKIP LOCKED`)
		if err != nil {
			return err
		}
		type pending struct {
			id      uuid.UUID
			topic   string
			payload json.RawMessage
		}
		var events []pending
		for rows.Next() {
			var event pending
			if err = rows.Scan(&event.id, &event.topic, &event.payload); err != nil {
				rows.Close()
				return err
			}
			events = append(events, event)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, event := range events {
			data, _ := json.Marshal(Envelope{event.id, event.payload})
			_, sendErr := client.EnqueueContext(ctx, asynq.NewTask(event.topic, data), asynq.TaskID(event.id.String()), asynq.MaxRetry(10), asynq.Retention(7*24*time.Hour))
			if sendErr != nil && !errors.Is(sendErr, asynq.ErrTaskIDConflict) {
				_, err = q.ExecContext(ctx, `UPDATE outbox_events SET attempts=attempts+1,last_error=$2,updated_at=now() WHERE id=$1`, event.id, sendErr.Error())
				if err != nil {
					return err
				}
				continue
			}
			if _, err = q.ExecContext(ctx, `UPDATE outbox_events SET dispatched_at=now(),attempts=attempts+1,last_error='',updated_at=now() WHERE id=$1`, event.id); err != nil {
				return err
			}
		}
		return nil
	})
}

func RunDispatcher(ctx context.Context, db *database.Database, client *asynq.Client) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := Dispatch(ctx, db, client); err != nil {
				slog.Error("outbox dispatch failed", "error", err)
			}
		}
	}
}
