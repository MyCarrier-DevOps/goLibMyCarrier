// Package teamsbottest provides a mock teamsbot.Sender for use in consumers'
// tests. It is excluded from coverage (its package name contains "test").
package teamsbottest

import (
	"context"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/teamsbot"
)

// MockCall records one SendToChannel invocation.
type MockCall struct {
	ChannelID string
	TenantID  string
	Activity  teamsbot.Activity
}

// MockActivityCall records one UpdateActivity or ReplyToActivity invocation.
type MockActivityCall struct {
	ConversationID string
	ActivityID     string
	Activity       teamsbot.Activity
}

// MockSender is a configurable teamsbot.ThreadSender for tests. Set the *Func
// fields to control the returns; inspect Calls, UpdateCalls and ReplyCalls to
// assert what was sent.
type MockSender struct {
	SendToChannelFunc func(ctx context.Context, channelID, tenantID string, act teamsbot.Activity) (teamsbot.SendResult, error)
	Calls             []MockCall

	UpdateActivityFunc func(ctx context.Context, conversationID, activityID string, act teamsbot.Activity) error
	UpdateCalls        []MockActivityCall

	ReplyToActivityFunc func(
		ctx context.Context, conversationID, activityID string, act teamsbot.Activity,
	) (teamsbot.SendResult, error)
	ReplyCalls []MockActivityCall
}

// SendToChannel records the call and delegates to SendToChannelFunc when set.
func (m *MockSender) SendToChannel(
	ctx context.Context,
	channelID, tenantID string,
	act teamsbot.Activity,
) (teamsbot.SendResult, error) {
	m.Calls = append(m.Calls, MockCall{ChannelID: channelID, TenantID: tenantID, Activity: act})
	if m.SendToChannelFunc != nil {
		return m.SendToChannelFunc(ctx, channelID, tenantID, act)
	}
	return teamsbot.SendResult{}, nil
}

// UpdateActivity records the call and delegates to UpdateActivityFunc when set.
func (m *MockSender) UpdateActivity(
	ctx context.Context,
	conversationID, activityID string,
	act teamsbot.Activity,
) error {
	m.UpdateCalls = append(
		m.UpdateCalls,
		MockActivityCall{ConversationID: conversationID, ActivityID: activityID, Activity: act},
	)
	if m.UpdateActivityFunc != nil {
		return m.UpdateActivityFunc(ctx, conversationID, activityID, act)
	}
	return nil
}

// ReplyToActivity records the call and delegates to ReplyToActivityFunc when set.
func (m *MockSender) ReplyToActivity(
	ctx context.Context,
	conversationID, activityID string,
	act teamsbot.Activity,
) (teamsbot.SendResult, error) {
	m.ReplyCalls = append(
		m.ReplyCalls,
		MockActivityCall{ConversationID: conversationID, ActivityID: activityID, Activity: act},
	)
	if m.ReplyToActivityFunc != nil {
		return m.ReplyToActivityFunc(ctx, conversationID, activityID, act)
	}
	return teamsbot.SendResult{}, nil
}

// Compile-time assertions that MockSender satisfies teamsbot.Sender and teamsbot.ThreadSender.
var (
	_ teamsbot.Sender       = (*MockSender)(nil)
	_ teamsbot.ThreadSender = (*MockSender)(nil)
)
