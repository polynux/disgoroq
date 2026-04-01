package logger

import (
	"go.uber.org/zap"
)

var Log *zap.Logger

var retentionDays int

func init() {
	rebuildLogger(GetConfig())
}

func Sync() {
	if Log != nil {
		_ = Log.Sync()
	}
}

func GetRetentionDays() int {
	return retentionDays
}
