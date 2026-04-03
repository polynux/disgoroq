package logger

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"polynux/disgoroq/database"
)

type eventLogger interface {
	LogEvent(ctx context.Context, event *database.BotEvent) error
}

var eventRepo eventLogger

func extractContextFromFields(fields []zap.Field) (guildID, channelID, messageID, userID string) {
	for _, field := range fields {
		switch field.Key {
		case "guild_id":
			if v, ok := field.Interface.(string); ok {
				guildID = v
			}
		case "channel_id":
			if v, ok := field.Interface.(string); ok {
				channelID = v
			}
		case "message_id":
			if v, ok := field.Interface.(string); ok {
				messageID = v
			}
		case "user_id":
			if v, ok := field.Interface.(string); ok {
				userID = v
			}
		}
	}
	return
}

func extractErrorFromFields(fields []zap.Field) string {
	for _, field := range fields {
		if field.Key == "error" || field.Type == zapcore.ErrorType {
			if err, ok := field.Interface.(error); ok && err != nil {
				return err.Error()
			}
		}
	}
	return ""
}

func createEventFromLog(ctx context.Context, level string, msg string, fields []zap.Field) *database.BotEvent {
	guildID, channelID, messageID, userID := extractContextFromFields(fields)
	errorMsg := ""

	if level == "error" {
		errorMsg = extractErrorFromFields(fields)
		if errorMsg == "" {
			errorMsg = msg
		}
	}

	eventType := mapLogLevelToEventType(level, errorMsg != "")

	return &database.BotEvent{
		Timestamp: time.Now(),
		EventType: eventType,
		GuildID:   guildID,
		ChannelID: channelID,
		MessageID: messageID,
		UserID:    userID,
		Error:     errorMsg,
	}
}

func mapLogLevelToEventType(level string, hasError bool) database.EventType {
	switch level {
	case "error":
		if hasError {
			return database.EventAICallFailed
		}
		return database.EventResponseFailed
	case "warn":
		return database.EventRateLimited
	case "info":
		return database.EventMessageReceived
	case "debug":
		return database.EventAICallStart
	default:
		return database.EventMessageReceived
	}
}

func SetEventRepository(repo eventLogger) {
	eventRepo = repo
}

func Debug(msg string, fields ...zap.Field) {
	if !IsEnabled() {
		return
	}
	Log.Debug(msg, fields...)

	if IsDBLoggingEnabled() && eventRepo != nil {
		dbLevel := GetDBLogLevel()
		if dbLevel == DBLogLevelDebug || dbLevel == DBLogLevelAll {
			ctx := context.Background()
			event := createEventFromLog(ctx, "debug", msg, fields)
			LogEvent(ctx, event)
		}
	}
}

func Info(msg string, fields ...zap.Field) {
	if !IsEnabled() {
		return
	}
	Log.Info(msg, fields...)

	if IsDBLoggingEnabled() && eventRepo != nil {
		guildID, _, _, _ := extractContextFromFields(fields)
		if guildID != "" {
			ctx := context.Background()
			event := createEventFromLog(ctx, "info", msg, fields)
			LogEvent(ctx, event)
		}
	}
}

func Warn(msg string, fields ...zap.Field) {
	if !IsEnabled() {
		return
	}
	Log.Warn(msg, fields...)

	if IsDBLoggingEnabled() && eventRepo != nil {
		ctx := context.Background()
		event := createEventFromLog(ctx, "warn", msg, fields)
		LogEvent(ctx, event)
	}
}

func Error(msg string, fields ...zap.Field) {
	if !IsEnabled() {
		return
	}

	if pc, file, line, ok := runtime.Caller(1); ok {
		funcName := runtime.FuncForPC(pc).Name()
		fields = append(fields,
			zap.String("caller", fmt.Sprintf("%s:%d", extractFileName(file), line)),
			zap.String("function", extractFunctionName(funcName)),
		)
	}

	Log.Error(msg, fields...)

	if IsDBLoggingEnabled() && eventRepo != nil {
		ctx := context.Background()
		event := createEventFromLog(ctx, "error", msg, fields)
		LogEvent(ctx, event)
	}
}

func Fatal(msg string, fields ...zap.Field) {
	if !IsEnabled() {
		return
	}

	if IsDBLoggingEnabled() && eventRepo != nil {
		ctx := context.Background()
		event := createEventFromLog(ctx, "error", msg, fields)
		event.EventType = database.EventResponseFailed
		LogEvent(ctx, event)
	}

	Log.Fatal(msg, fields...)
}

func LogEvent(ctx context.Context, event *database.BotEvent) {
	if !IsEventLoggingEnabled() || eventRepo == nil {
		return
	}

	if !ShouldLogToDB(event.EventType, GetDBLogLevel()) {
		return
	}

	if err := eventRepo.LogEvent(ctx, event); err != nil {
		Log.Error("Failed to log event to database",
			zap.Error(err),
			zap.String("event_type", string(event.EventType)),
			zap.String("guild_id", event.GuildID),
		)
	}
}

func LogMessageEvent(ctx context.Context, eventType database.EventType, guildID, channelID, messageID, userID string, details *database.EventDetails) {
	if !IsEventLoggingEnabled() || eventRepo == nil {
		return
	}

	event := &database.BotEvent{
		Timestamp: time.Now(),
		EventType: eventType,
		GuildID:   guildID,
		ChannelID: channelID,
		MessageID: messageID,
		UserID:    userID,
		Details:   details,
	}

	LogEvent(ctx, event)
}

func extractFileName(file string) string {
	if idx := strings.LastIndex(file, "/"); idx != -1 {
		return file[idx+1:]
	}
	return file
}

func extractFunctionName(funcName string) string {
	if idx := strings.LastIndex(funcName, "."); idx != -1 {
		return funcName[idx+1:]
	}
	return funcName
}
