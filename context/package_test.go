package context

import (
	"testing"
	"time"
)

func TestMessage(t *testing.T) {
	ctx, cancel := Message()
	defer cancel()

	if ctx == nil {
		t.Error("Message() returned nil context")
	}
	if cancel == nil {
		t.Error("Message() returned nil cancel function")
	}

	// Verify context has deadline
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Error("Message() context has no deadline")
	}
	if time.Until(deadline) > MessageTimeout {
		t.Errorf("Message() deadline exceeds MessageTimeout: got %v, want <= %v", time.Until(deadline), MessageTimeout)
	}
}

func TestCommand(t *testing.T) {
	ctx, cancel := Command()
	defer cancel()

	if ctx == nil {
		t.Error("Command() returned nil context")
	}
	if cancel == nil {
		t.Error("Command() returned nil cancel function")
	}

	// Verify context has deadline
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Error("Command() context has no deadline")
	}
	if time.Until(deadline) > CommandTimeout {
		t.Errorf("Command() deadline exceeds CommandTimeout: got %v, want <= %v", time.Until(deadline), CommandTimeout)
	}
}

func TestAI(t *testing.T) {
	ctx, cancel := AI()
	defer cancel()

	if ctx == nil {
		t.Error("AI() returned nil context")
	}
	if cancel == nil {
		t.Error("AI() returned nil cancel function")
	}

	// Verify context has deadline
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Error("AI() context has no deadline")
	}
	if time.Until(deadline) > AITimeout {
		t.Errorf("AI() deadline exceeds AITimeout: got %v, want <= %v", time.Until(deadline), AITimeout)
	}
}

func TestDatabase(t *testing.T) {
	ctx, cancel := Database()
	defer cancel()

	if ctx == nil {
		t.Error("Database() returned nil context")
	}
	if cancel == nil {
		t.Error("Database() returned nil cancel function")
	}

	// Verify context has deadline
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Error("Database() context has no deadline")
	}
	if time.Until(deadline) > DatabaseTimeout {
		t.Errorf("Database() deadline exceeds DatabaseTimeout: got %v, want <= %v", time.Until(deadline), DatabaseTimeout)
	}
}

func TestCancellationWorks(t *testing.T) {
	ctx, cancel := Message()
	cancel()

	select {
	case <-ctx.Done():
		// Expected: context should be done after cancel
	default:
		t.Error("Context not done after cancel() called")
	}
}

func TestTimeoutExpiry(t *testing.T) {
	ctx, cancel := Database()
	defer cancel()

	select {
	case <-time.After(DatabaseTimeout - 100*time.Millisecond):
		// Expected: context should still be alive slightly before the deadline.
	case <-ctx.Done():
		t.Error("Context done too early")
	}

	select {
	case <-ctx.Done():
		// Expected: context should be done after timeout
	case <-time.After(500 * time.Millisecond):
		t.Error("Context not done after timeout expired")
	}
}
