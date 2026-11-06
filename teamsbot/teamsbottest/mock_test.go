package teamsbottest

import (
	"context"
	"errors"
	"reflect"
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
	m := &MockSender{
		SendToChannelFunc: func(context.Context, string, string, teamsbot.Activity) (teamsbot.SendResult, error) {
			return teamsbot.SendResult{}, want
		},
	}
	if _, err := m.SendToChannel(context.Background(), "c", "t", teamsbot.TextActivity("x")); !errors.Is(err, want) {
		t.Fatalf("override not used: %v", err)
	}
}
func TestMockSender_ThreadCallsDefaultToSuccess(t *testing.T) {
	m := &MockSender{}
	act := teamsbot.TextActivity("hi")

	if err := m.UpdateActivity(context.Background(), "c1", "a1", act); err != nil {
		t.Fatalf("UpdateActivity: %v", err)
	}
	res, err := m.ReplyToActivity(context.Background(), "c2", "a2", act)
	if err != nil || res != (teamsbot.SendResult{}) {
		t.Fatalf("ReplyToActivity = %+v, %v", res, err)
	}
	if !reflect.DeepEqual(m.UpdateCalls, []MockActivityCall{{ConversationID: "c1", ActivityID: "a1", Activity: act}}) {
		t.Errorf("UpdateCalls = %+v", m.UpdateCalls)
	}
	if !reflect.DeepEqual(m.ReplyCalls, []MockActivityCall{{ConversationID: "c2", ActivityID: "a2", Activity: act}}) {
		t.Errorf("ReplyCalls = %+v", m.ReplyCalls)
	}
}

func TestMockSender_ThreadCallsUseFuncs(t *testing.T) {
	boom := errors.New("boom")
	m := &MockSender{
		UpdateActivityFunc: func(context.Context, string, string, teamsbot.Activity) error { return boom },
		ReplyToActivityFunc: func(context.Context, string, string, teamsbot.Activity) (teamsbot.SendResult, error) {
			return teamsbot.SendResult{ActivityID: "r"}, boom
		},
	}
	if err := m.UpdateActivity(context.Background(), "c", "a", teamsbot.Activity{}); !errors.Is(err, boom) {
		t.Errorf("UpdateActivity err = %v", err)
	}
	res, err := m.ReplyToActivity(context.Background(), "c", "a", teamsbot.Activity{})
	if !errors.Is(err, boom) || res.ActivityID != "r" {
		t.Errorf("ReplyToActivity = %+v, %v", res, err)
	}
}
