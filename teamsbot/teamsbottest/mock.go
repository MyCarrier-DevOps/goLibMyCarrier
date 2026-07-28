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

// MockSender is a configurable teamsbot.Sender for tests. Set SendToChannelFunc
// to control the return; inspect Calls to assert what was sent.
type MockSender struct {
	SendToChannelFunc func(ctx context.Context, channelID, tenantID string, act teamsbot.Activity) (teamsbot.SendResult, error)
	Calls             []MockCall
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

// Compile-time assertion that MockSender satisfies teamsbot.Sender.
var _ teamsbot.Sender = (*MockSender)(nil)
