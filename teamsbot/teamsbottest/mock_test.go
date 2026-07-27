package teamsbottest

import (
	"context"
	"errors"
	"testing"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/teamsbot"
)

func TestMockSenderRecordsCalls(t *testing.T) {
	var s teamsbot.Sender = &MockSender{}
	_, err := s.SendToChannel(context.Background(), "19:abc", "t", teamsbot.TextActivity("hi"))
	if err != nil {
		t.Fatalf("default returns nil error, got %v", err)
	}
	m := s.(*MockSender)
	if len(m.Calls) != 1 || m.Calls[0].ChannelID != "19:abc" {
		t.Fatalf("call not recorded: %+v", m.Calls)
	}
}

func TestMockSenderFuncOverride(t *testing.T) {
	want := errors.New("boom")
	m := &MockSender{SendToChannelFunc: func(context.Context, string, string, teamsbot.Activity) (teamsbot.SendResult, error) {
		return teamsbot.SendResult{}, want
	}}
	if _, err := m.SendToChannel(context.Background(), "c", "t", teamsbot.TextActivity("x")); !errors.Is(err, want) {
		t.Fatalf("override not used: %v", err)
	}
}
