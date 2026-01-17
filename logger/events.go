package logger

import (
	"context"
	"database/sql"

	"go.uber.org/zap"
	"polynux/disgoroq/database"
)

type EventRepository struct {
	*database.EventRepository
}

func NewEventRepository(db *sql.DB, log *zap.Logger) *EventRepository {
	return &EventRepository{
		EventRepository: database.NewEventRepository(db, log),
	}
}

func (r *EventRepository) LogEvent(ctx context.Context, event *database.BotEvent) error {
	return r.EventRepository.LogEvent(ctx, event)
}

func (r *EventRepository) GetEvents(ctx context.Context, guildID string, limit int) ([]database.BotEvent, error) {
	return r.EventRepository.GetEvents(ctx, guildID, limit)
}

func (r *EventRepository) GetEventsByType(ctx context.Context, eventType database.EventType, limit int) ([]database.BotEvent, error) {
	return r.EventRepository.GetEventsByType(ctx, eventType, limit)
}

func (r *EventRepository) GetEventsByMessage(ctx context.Context, messageID string) ([]database.BotEvent, error) {
	return r.EventRepository.GetEventsByMessage(ctx, messageID)
}

func (r *EventRepository) DeleteOldEvents(ctx context.Context, days int) error {
	return r.EventRepository.DeleteOldEvents(ctx, days)
}
