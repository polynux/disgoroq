package logger

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var Log *zap.Logger

var retentionDays int

func init() {
	cfg := GetConfig()

	if !cfg.Enabled {
		Log = zap.NewNop()
		retentionDays = cfg.RetentionDays
		return
	}

	var zapConfig zap.Config
	if cfg.Encoding == "console" {
		zapConfig = zap.NewDevelopmentConfig()
		zapConfig.Encoding = "console"
	} else {
		zapConfig = zap.NewProductionConfig()
	}

	zapLevel, err := zapcore.ParseLevel(cfg.Level)
	if err != nil {
		zapLevel = zapcore.InfoLevel
	}
	zapConfig.Level = zap.NewAtomicLevelAt(zapLevel)

	Log, err = zapConfig.Build()
	if err != nil {
		panic(err)
	}

	retentionDays = cfg.RetentionDays

	Log.Info("Logger initialized",
		zap.String("encoding", cfg.Encoding),
		zap.String("level", cfg.Level),
		zap.Int("event_retention_days", retentionDays),
		zap.Bool("enabled", cfg.Enabled),
		zap.Bool("log_to_db", cfg.LogToDB),
		zap.Bool("event_logging_enabled", cfg.EventLoggingEnabled),
		zap.String("db_log_level", GetDBLogLevelName(cfg.DBLogLevel)),
	)
}

func Sync() {
	if Log != nil {
		_ = Log.Sync()
	}
}

func GetRetentionDays() int {
	return retentionDays
}
