package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"go.uber.org/zap"
	sqlcdb "polynux/disgoroq/db"
)

type EventType string

const (
	EventMessageReceived  EventType = "message_received"
	EventThresholdSkipped EventType = "threshold_skipped"
	EventStateOff         EventType = "state_off"
	EventRateLimited      EventType = "rate_limited"
	EventContextBuilt     EventType = "context_built"
	EventContextFailed    EventType = "context_failed"
	EventAICallStart      EventType = "ai_call_start"
	EventAICallSuccess    EventType = "ai_call_success"
	EventAICallFailed     EventType = "ai_call_failed"
	EventEmptyResponse    EventType = "empty_response"
	EventResponseSent     EventType = "response_sent"
	EventResponseFailed   EventType = "response_failed"

	// New events for retry and fallback tracking
	EventAIRetrying     EventType = "ai_retrying"
	EventAIFallback     EventType = "ai_fallback"
	EventProviderSwitch EventType = "provider_switch"
)

type EventDetails struct {
	MessagesCount int     `json:"messages_count,omitempty"`
	ImageCount    int     `json:"image_count,omitempty"`
	Threshold     float64 `json:"threshold,omitempty"`
	RandValue     float64 `json:"rand_value,omitempty"`
	Temperature   float32 `json:"temperature,omitempty"`
	Model         string  `json:"model,omitempty"`
	Context       string  `json:"context,omitempty"`
}

type BotEvent struct {
	Timestamp  time.Time
	EventType  EventType
	GuildID    string
	ChannelID  string
	MessageID  string
	UserID     string
	Details    *EventDetails
	DurationMS int64
	Error      string
}

type EventRepository struct {
	db      *sql.DB
	log     *zap.Logger
	queries *sqlcdb.Queries
}

func NewEventRepository(db *sql.DB, log *zap.Logger) *EventRepository {
	return &EventRepository{
		db:      db,
		log:     log,
		queries: sqlcdb.New(db),
	}
}

func (r *EventRepository) LogEvent(ctx context.Context, event *BotEvent) error {
	detailsJSON := ""
	if event.Details != nil {
		jsonBytes, err := json.Marshal(event.Details)
		if err != nil {
			r.log.Error("Failed to marshal event details",
				zap.Error(err),
				zap.String("event_type", string(event.EventType)),
			)
			detailsJSON = "{}"
		} else {
			detailsJSON = string(jsonBytes)
		}
	}

	params := sqlcdb.InsertEventParams{
		Timestamp:  event.Timestamp.Unix(),
		EventType:  string(event.EventType),
		GuildID:    sql.NullString{String: event.GuildID, Valid: event.GuildID != ""},
		ChannelID:  sql.NullString{String: event.ChannelID, Valid: event.ChannelID != ""},
		MessageID:  sql.NullString{String: event.MessageID, Valid: event.MessageID != ""},
		UserID:     sql.NullString{String: event.UserID, Valid: event.UserID != ""},
		Details:    sql.NullString{String: detailsJSON, Valid: detailsJSON != ""},
		DurationMs: sql.NullInt64{Int64: event.DurationMS, Valid: event.DurationMS > 0},
		Error:      sql.NullString{String: event.Error, Valid: event.Error != ""},
	}

	if err := r.queries.InsertEvent(ctx, params); err != nil {
		r.log.Error("Failed to insert event",
			zap.Error(err),
			zap.String("event_type", string(event.EventType)),
			zap.String("guild_id", event.GuildID),
		)
		return err
	}

	return nil
}

func (r *EventRepository) GetEvents(ctx context.Context, guildID string, limit int) ([]BotEvent, error) {
	var whereClause string
	var args []interface{}

	args = append(args, limit)

	if guildID != "" {
		whereClause = "WHERE guild_id = ?"
		args = append([]interface{}{guildID}, args...)
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT timestamp, event_type, guild_id, channel_id, message_id, user_id, details, duration_ms, error
		FROM bot_events
		`+whereClause+`
		ORDER BY timestamp DESC
		LIMIT ?
	`, args...)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []BotEvent{}
	for rows.Next() {
		var event BotEvent
		var timestamp int64
		var eventType string
		var detailsJSON string
		var durationMS sql.NullInt64
		var errorText sql.NullString

		err := rows.Scan(&timestamp, &eventType, &event.GuildID, &event.ChannelID, &event.MessageID, &event.UserID, &detailsJSON, &durationMS, &errorText)
		if err != nil {
			return nil, err
		}

		event.Timestamp = time.Unix(timestamp, 0)
		event.EventType = EventType(eventType)
		event.DurationMS = durationMS.Int64
		event.Error = errorText.String

		if detailsJSON != "" {
			var details EventDetails
			if err := json.Unmarshal([]byte(detailsJSON), &details); err == nil {
				event.Details = &details
			}
		}

		events = append(events, event)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return events, nil
}

func (r *EventRepository) GetEventsByType(ctx context.Context, eventType EventType, limit int) ([]BotEvent, error) {
	dbLimit := int64(limit)

	rows, err := r.db.QueryContext(ctx, `
		SELECT timestamp, event_type, guild_id, channel_id, message_id, user_id, details, duration_ms, error
		FROM bot_events
		WHERE event_type = ?
		ORDER BY timestamp DESC
		LIMIT ?
	`, string(eventType), dbLimit)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []BotEvent{}
	for rows.Next() {
		var event BotEvent
		var timestamp int64
		var eventTypeStr string
		var detailsJSON string
		var durationMS sql.NullInt64
		var errorText sql.NullString

		err := rows.Scan(&timestamp, &eventTypeStr, &event.GuildID, &event.ChannelID, &event.MessageID, &event.UserID, &detailsJSON, &durationMS, &errorText)
		if err != nil {
			return nil, err
		}

		event.Timestamp = time.Unix(timestamp, 0)
		event.EventType = EventType(eventTypeStr)
		event.DurationMS = durationMS.Int64
		event.Error = errorText.String

		if detailsJSON != "" {
			var details EventDetails
			if err := json.Unmarshal([]byte(detailsJSON), &details); err == nil {
				event.Details = &details
			}
		}

		events = append(events, event)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return events, nil
}

func (r *EventRepository) GetEventsByMessage(ctx context.Context, messageID string) ([]BotEvent, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT timestamp, event_type, guild_id, channel_id, message_id, user_id, details, duration_ms, error
		FROM bot_events
		WHERE message_id = ?
		ORDER BY timestamp ASC
	`, messageID)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []BotEvent{}
	for rows.Next() {
		var event BotEvent
		var timestamp int64
		var eventTypeStr string
		var detailsJSON string
		var durationMS sql.NullInt64
		var errorText sql.NullString

		err := rows.Scan(&timestamp, &eventTypeStr, &event.GuildID, &event.ChannelID, &event.MessageID, &event.UserID, &detailsJSON, &durationMS, &errorText)
		if err != nil {
			return nil, err
		}

		event.Timestamp = time.Unix(timestamp, 0)
		event.EventType = EventType(eventTypeStr)
		event.DurationMS = durationMS.Int64
		event.Error = errorText.String

		if detailsJSON != "" {
			var details EventDetails
			if err := json.Unmarshal([]byte(detailsJSON), &details); err == nil {
				event.Details = &details
			}
		}

		events = append(events, event)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return events, nil
}

func (r *EventRepository) DeleteOldEvents(ctx context.Context, days int) error {
	cutoff := time.Now().AddDate(0, 0, -days).Unix()

	result, err := r.db.ExecContext(ctx, `
		DELETE FROM bot_events
		WHERE timestamp < ?
	`, cutoff)

	if err != nil {
		r.log.Error("Failed to delete old events",
			zap.Error(err),
			zap.Int("days", days),
		)
		return err
	}

	rowsAffected, _ := result.RowsAffected()
	r.log.Info("Deleted old events",
		zap.Int64("rows_deleted", rowsAffected),
		zap.Int("retention_days", days),
	)

	return nil
}
