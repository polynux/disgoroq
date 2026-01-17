package logger

import (
	"os"
	"strconv"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var Log *zap.Logger

var retentionDays int

func init() {
	encoding := getEnv("LOG_ENCODING", "json")
	level := getEnv("LOG_LEVEL", "info")
	retentionStr := getEnv("EVENT_RETENTION_DAYS", "7")

	retentionDaysInt, err := strconv.Atoi(retentionStr)
	if err != nil {
		retentionDaysInt = 7
	}
	retentionDays = retentionDaysInt

	var zapConfig zap.Config
	if encoding == "console" {
		zapConfig = zap.NewDevelopmentConfig()
		zapConfig.Encoding = "console"
	} else {
		zapConfig = zap.NewProductionConfig()
	}

	zapLevel, err := zapcore.ParseLevel(level)
	if err != nil {
		zapLevel = zapcore.InfoLevel
	}
	zapConfig.Level = zap.NewAtomicLevelAt(zapLevel)

	Log, err = zapConfig.Build()
	if err != nil {
		panic(err)
	}

	Log.Info("Logger initialized",
		zap.String("encoding", encoding),
		zap.String("level", level),
		zap.Int("event_retention_days", retentionDays),
	)
}

func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

func Sync() {
	if Log != nil {
		_ = Log.Sync()
	}
}

func GetRetentionDays() int {
	return retentionDays
}
