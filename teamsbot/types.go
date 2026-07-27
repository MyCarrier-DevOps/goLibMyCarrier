package teamsbot

import "encoding/json"

// AdaptiveCardContentType is the attachment content type for an Adaptive Card.
const AdaptiveCardContentType = "application/vnd.microsoft.card.adaptive"

// Activity is a Bot Framework message activity. Callers build one with
// AdaptiveCardActivity or TextActivity.
type Activity struct {
	Type        string       `json:"type"`
	Text        string       `json:"text,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Attachment carries a card (or other content) on an Activity. Content is opaque
// JSON built by the caller — this module never inspects it.
type Attachment struct {
	ContentType string          `json:"contentType"`
	Content     json.RawMessage `json:"content"`
}

// AdaptiveCardActivity builds a "message" Activity carrying a single Adaptive
// Card attachment. card is the raw Adaptive Card JSON built by the caller.
func AdaptiveCardActivity(card json.RawMessage) Activity {
	return Activity{
		Type: "message",
		Attachments: []Attachment{{
			ContentType: AdaptiveCardContentType,
			Content:     card,
		}},
	}
}

// TextActivity builds a plain-text "message" Activity.
func TextActivity(text string) Activity {
	return Activity{Type: "message", Text: text}
}

// conversationParameters is the create-conversation request body for posting to
// a Teams channel.
type conversationParameters struct {
	IsGroup     bool        `json:"isGroup"`
	ChannelData channelData `json:"channelData"`
	Activity    Activity    `json:"activity"`
}

type channelData struct {
	Channel channelInfo `json:"channel"`
	Tenant  tenantInfo  `json:"tenant"`
}

type channelInfo struct {
	ID string `json:"id"`
}

type tenantInfo struct {
	ID string `json:"id"`
}

// SendResult is the Bot Connector response to a successful create-conversation.
type SendResult struct {
	ConversationID string `json:"id"`
	ActivityID     string `json:"activityId"`
}
