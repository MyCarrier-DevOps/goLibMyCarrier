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

`TEAMS_SERVICE_URL` must be an absolute `https://` URL with no query or
fragment; `LoadConfig`/`LoadConfigFromViper` reject anything else.

### Constraints

- **Single-tenant only.** The client acquires its token from
  `https://login.microsoftonline.com/{tenant}/oauth2/v2.0/token` using the
  app's `TEAMS_BOT_TENANT_ID`. Multi-tenant Bot Framework apps authenticate
  against the `botframework.com` authority instead — that flow is not
  implemented, so a multi-tenant app registration will fail to authenticate
  against the Bot Connector through this client.
- **Each `SendToChannel` call starts a new conversation thread.** The Bot
  Connector's create-conversation call always opens a fresh thread in the
  channel; there is no "reply in place" for the first message. Keep the
  returned `SendResult.ConversationID` and `SendResult.ActivityID` if you need
  to reply to or update that specific message later — this client does not
  persist them for you.
- **Sovereign clouds use a different `TEAMS_SERVICE_URL`.** The default value
  only reaches the Microsoft Teams public cloud. For government/sovereign
  clouds, set `TEAMS_SERVICE_URL` explicitly:

  | Cloud | `TEAMS_SERVICE_URL` |
  |-------|----------------------|
  | Public (default) | `https://smba.trafficmanager.net/teams` |
  | GCC | `https://smba.infra.gcc.teams.microsoft.com/teams` |
  | GCC High | `https://smba.infra.gov.teams.microsoft.us/teams` |
  | DoD | `https://smba.infra.dod.teams.microsoft.us/teams` |

Testing: use `teamsbottest.MockSender`.
