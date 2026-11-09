package teamsbot

import (
	"encoding/json"
	"testing"
)

func TestAdaptiveCardActivity(t *testing.T) {
	card := json.RawMessage(`{"type":"AdaptiveCard","version":"1.5"}`)
	act := AdaptiveCardActivity(card)

	if act.Type != "message" {
		t.Fatalf("Type = %q", act.Type)
	}
	if len(act.Attachments) != 1 {
		t.Fatalf("want 1 attachment, got %d", len(act.Attachments))
	}
	if act.Attachments[0].ContentType != AdaptiveCardContentType {
		t.Errorf("ContentType = %q", act.Attachments[0].ContentType)
	}

	// Round-trips to JSON with the card content intact.
	b, err := json.Marshal(act)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !json.Valid(b) {
		t.Fatal("activity did not marshal to valid JSON")
	}
}

func TestTextActivity(t *testing.T) {
	act := TextActivity("hello")
	if act.Type != "message" || act.Text != "hello" || act.Attachments != nil {
		t.Fatalf("got %+v", act)
	}
}

func TestActivityJSON_OmitsEmptyIDFields(t *testing.T) {
	b, err := json.Marshal(AdaptiveCardActivity(json.RawMessage(`{"type":"AdaptiveCard"}`)))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"id", "replyToId"} {
		if _, ok := m[k]; ok {
			t.Errorf("%q present in %s", k, b)
		}
	}
}

func TestActivityJSON_IncludesSetIDFields(t *testing.T) {
	b, err := json.Marshal(Activity{Type: "message", ID: "a1", ReplyToID: "r1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["id"] != "a1" || m["replyToId"] != "r1" {
		t.Errorf("got %s", b)
	}
}
