package handlers

import (
	"testing"
)

func TestTryAcquireReplySlotIsBounded(t *testing.T) {
	h := &MessageHandler{replySlots: make(chan struct{}, 1)}

	if !h.tryAcquireReplySlot() {
		t.Fatal("expected the first reply slot acquisition to succeed")
	}
	if h.tryAcquireReplySlot() {
		t.Fatal("expected acquisition to fail while the slot is occupied")
	}

	<-h.replySlots
	if !h.tryAcquireReplySlot() {
		t.Fatal("expected acquisition to succeed after the slot is released")
	}
}

func TestLockForSharesOneLockPerKey(t *testing.T) {
	h := &MessageHandler{}

	first := h.lockFor(&h.replyLocks, "1")
	if first != h.lockFor(&h.replyLocks, "1") {
		t.Fatal("expected the same key to share a lock")
	}
	if first == h.lockFor(&h.replyLocks, "2") {
		t.Fatal("expected different keys to use separate locks")
	}
}
