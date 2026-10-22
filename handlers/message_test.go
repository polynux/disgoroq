package handlers

import (
	"testing"

	"github.com/disgoorg/snowflake/v2"
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

func TestReplyLockForChannelSerializesTheSameChannel(t *testing.T) {
	h := &MessageHandler{}

	first := h.replyLockForChannel(snowflake.ID(1))
	if first != h.replyLockForChannel(snowflake.ID(1)) {
		t.Fatal("expected the same channel to share a reply lock")
	}
	if first == h.replyLockForChannel(snowflake.ID(2)) {
		t.Fatal("expected different channels to use separate reply locks")
	}
}
