package context

import (
	"context"
	"time"
)

const (
	DefaultTimeout    = 30 * time.Second
	MessageTimeout    = 30 * time.Second
	CommandTimeout    = 15 * time.Second
	AITimeout         = 60 * time.Second
	DatabaseTimeout   = 10 * time.Second
)

// Message returns a context with MessageTimeout
func Message() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), MessageTimeout)
}

// Command returns a context with CommandTimeout
func Command() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), CommandTimeout)
}

// AI returns a context with AITimeout
func AI() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), AITimeout)
}

// Database returns a context with DatabaseTimeout
func Database() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), DatabaseTimeout)
}
