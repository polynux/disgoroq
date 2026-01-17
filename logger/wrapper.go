package logger

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"go.uber.org/zap"
	"polynux/disgoroq/database"
)

var eventRepo *EventRepository

func SetEventRepository(repo *EventRepository) {
	eventRepo = repo
}

func Debug(msg string, fields ...zap.Field) {
	if !IsEnabled() {
		return
	}
	Log.Debug(msg, fields...)
}

func Info(msg string, fields ...zap.Field) {
	if !IsEnabled() {
		return
	}
	Log.Info(msg, fields...)
}

func Warn(msg string, fields ...zap.Field) {
	if !IsEnabled() {
		return
	}
	Log.Warn(msg, fields...)
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
}

func Fatal(msg string, fields ...zap.Field) {
	if !IsEnabled() {
		return
	}
	Log.Fatal(msg, fields...)
}

func LogEvent(ctx context.Context, event *database.BotEvent) {
	if !IsEventLoggingEnabled() || eventRepo == nil {
		return
	}

	if err := eventRepo.LogEvent(ctx, event); err != nil {
		Error("Failed to log event to database",
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
