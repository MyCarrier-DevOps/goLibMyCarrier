# teamsbot

Reusable Go client for the Microsoft Bot Connector REST API — send-only
proactive messaging to Microsoft Teams channels. It acquires a
client-credentials token and posts an `Activity` (optionally carrying an
Adaptive Card) to a channel. Polly-agnostic: the card is opaque `json.RawMessage`.

## Usage

```go
c, err := teamsbot.NewClientFromEnv() // TEAMS_BOT_APP_ID/SECRET/TENANT_ID (+ TEAMS_SERVICE_URL)
if err != nil {
    return err
}
card := json.RawMessage(`{"type":"AdaptiveCard","version":"1.5", ...}`)
_, err = c.SendToChannel(ctx, "19:...@thread.tacv2", tenantID, teamsbot.AdaptiveCardActivity(card))
```

## Config

| Env | Required | Default |
|-----|----------|---------|
| `TEAMS_BOT_APP_ID` | yes | — |
| `TEAMS_BOT_APP_SECRET` | yes | — |
| `TEAMS_BOT_TENANT_ID` | yes | — |
| `TEAMS_SERVICE_URL` | no | `https://smba.trafficmanager.net/teams` |

Testing: use `teamsbottest.MockSender`.
