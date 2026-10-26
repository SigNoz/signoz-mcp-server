# Notification channel request examples

Source: request examples published by the SigNoz OpenAPI document at 09bba8960dfc3e0a43dc54d6d7cf203aae9841f4.

## config.spec fields by kind

`config` is `{kind, spec}`. The kinds below are every value SigNoz accepts in this release; `spec` fields marked required must be present. Fields marked secret are returned by create, update and get by ID, never by list.

### email

- `headers` object: Extra SMTP headers keyed by canonical MIME header name, such as Subject.
- `html` string: Alertmanager template for the HTML body; defaults to the Alertmanager email default.
- `sendResolved` boolean: Also notify when the alert resolves. Defaults to false.
- `to` string (required): The recipient addresses, comma separated.

### googlechat

- `sendResolved` boolean: Also notify when the alert resolves. Defaults to false.
- `text` string: Alertmanager template for the message body; defaults to the SigNoz Google Chat default.
- `title` string: Alertmanager template for the message title; defaults to the SigNoz Google Chat default.
- `webhookUrl` string (secret) (required): The Google Chat space webhook URL. A secret; returned by create, update and fetch by ID, never by list.

### incidentio

- `description` string: Alertmanager template for the alert description, rendered as markdown; defaults to the SigNoz incident.io default.
- `metadata` object: Extra event metadata keyed by name, merged over the group's common labels; values are Alertmanager templates.
- `sendResolved` boolean: Also notify when the alert resolves. Defaults to false.
- `title` string: Alertmanager template for the alert title; defaults to the SigNoz incident.io default.
- `token` string (secret) (required): The alert source secret token, without the Bearer prefix. A secret; returned by create, update and fetch by ID, never by list.
- `url` string (required): The alert source HTTP alert events endpoint copied from its setup page.

### jira

- `apiToken` string (secret) (required): The Atlassian API token paired with email. A secret; returned by create, update and fetch by ID, never by list.
- `customFields` object: Extra issue fields keyed by Jira field ID. Stored, but not currently included in the issue Jira receives.
- `description` string: Alertmanager template for the issue description, rendered as markdown; defaults to the SigNoz Jira default.
- `email` string (required): The Atlassian account email used for basic auth.
- `issueType` string (required): The issue type name, such as Bug.
- `labels` array of string: Labels added to every issue.
- `priority` string: The priority name assigned to new issues.
- `project` string (required): The key of the project issues are created in.
- `reopenDuration` string: How long after resolution a firing alert reopens the issue instead of creating a new one, as a Prometheus duration in canonical form such as 3d; defaults to 3d.
- `reopenTransition` string: The transition applied when an alert fires again on a resolved issue.
- `resolveTransition` string: The transition applied to the issue when the alert resolves.
- `sendResolved` boolean: Also notify when the alert resolves. Defaults to false.
- `site` string (required): The Jira Cloud base URL, https://<site>.atlassian.net; only Jira Cloud is supported.
- `summary` string: Alertmanager template for the issue summary; defaults to the SigNoz Jira default.
- `wontFixResolution` string: The resolution name that stops an issue from being reopened.

### jsmops

- `apiKey` string (secret) (required): The JSM Ops integration API key. A secret; returned by create, update and fetch by ID, never by list.
- `description` string: Alertmanager template for the alert description; defaults to the SigNoz JSM Ops default.
- `message` string: Alertmanager template for the alert message; defaults to the SigNoz JSM Ops default.
- `priority` string: The alert priority, P1 to P5.
- `sendResolved` boolean: Also notify when the alert resolves. Defaults to false.
- `tags` string: Comma-separated tags attached to the alert; defaults to signoz.

### msteams

- `sendResolved` boolean: Also notify when the alert resolves. Defaults to true.
- `text` string: Alertmanager template for the card body; defaults to the Alertmanager Teams default.
- `title` string: Alertmanager template for the card title; defaults to the Alertmanager Teams default.
- `webhookUrl` string (secret) (required): The Teams incoming webhook URL. A secret; returned by create, update and fetch by ID, never by list.

### opsgenie

- `apiKey` string (secret) (required): The Opsgenie API integration key. A secret; returned by create, update and fetch by ID, never by list.
- `apiUrl` string: The Opsgenie API host; empty uses the global Alertmanager default.
- `description` string: Alertmanager template for the alert description; defaults to the Alertmanager Opsgenie default.
- `details` object: Custom alert details keyed by name, each an Alertmanager template.
- `message` string: Alertmanager template for the alert message; defaults to the Alertmanager Opsgenie default.
- `priority` string: The alert priority, P1 to P5.
- `sendResolved` boolean: Also notify when the alert resolves. Defaults to true.
- `source` string: Alertmanager template for the alert source; defaults to the Alertmanager Opsgenie default.

### pagerduty

- `class` string: The class or type of the event.
- `client` string: Alertmanager template for the client identifier; defaults to the Alertmanager PagerDuty default.
- `clientUrl` string: Alertmanager template for the client URL; defaults to the Alertmanager PagerDuty default.
- `component` string: The component of the source machine that is responsible for the event.
- `description` string: Alertmanager template for the incident summary; defaults to the Alertmanager PagerDuty default.
- `details` object: Custom details keyed by name, each an Alertmanager template; defaults to the Alertmanager firing and resolved summary set.
- `group` string: The logical grouping of components the event belongs to.
- `routingKey` string (secret) (required): The Events API v2 integration key. A secret; returned by create, update and fetch by ID, never by list.
- `sendResolved` boolean: Also notify when the alert resolves. Defaults to true.
- `severity` string: The incident severity: critical, error, warning or info.
- `source` string: Alertmanager template for the unique location of the affected system; defaults to client.
- `url` string: The Events API endpoint; empty uses the PagerDuty default.

### slack

- `actions` array of AlertmanagertypesChannelSlackAction: Attachment buttons; each requires type and text plus url or name.
  - `confirm` AlertmanagertypesChannelSlackConfirmation: A confirmation dialog shown before the action runs.
    - `dismissText` string: The label of the cancel button.
    - `okText` string: The label of the confirm button.
    - `text` string (required): The dialog body.
    - `title` string: The dialog title.
  - `name` string: The action name Slack posts back for a message button; required unless url is set.
  - `style` string: The button style: default, primary or danger.
  - `text` string (required): The button label.
  - `type` string (required): The Slack action type, normally button.
  - `url` string: The link the button opens; required unless name is set.
  - `value` string: The value Slack posts back with a message button.
- `apiUrl` string (secret) (required): The Slack incoming webhook URL. A secret; returned by create, update and fetch by ID, never by list.
- `channel` string: The channel or user to post to, such as #alerts; empty uses the webhook default.
- `color` string: Alertmanager template for the attachment color; defaults to the Alertmanager Slack default.
- `fallback` string: Alertmanager template for the plain-text summary shown where attachments cannot render; defaults to the Alertmanager Slack default.
- `fields` array of AlertmanagertypesChannelSlackField: Attachment fields appended to the message; each requires title and value.
  - `short` boolean: Render the field side by side with other short fields.
  - `title` string (required): The field title.
  - `value` string (required): The field value; an Alertmanager template.
- `footer` string: Alertmanager template for the attachment footer; defaults to the Alertmanager Slack default.
- `pretext` string: Alertmanager template for the text shown above the attachment; defaults to the Alertmanager Slack default.
- `sendResolved` boolean: Also notify when the alert resolves. Defaults to false.
- `text` string: Alertmanager template for the message body; defaults to the Alertmanager Slack default.
- `title` string: Alertmanager template for the message title; defaults to the Alertmanager Slack default.
- `titleLink` string: Alertmanager template for the link the title opens; defaults to the Alertmanager Slack default.

### telegram

- `apiUrl` string
- `botToken` string (secret) (required)
- `chatId` integer (required)
- `disableNotifications` boolean
- `message` string
- `messageThreadId` integer
- `sendResolved` boolean
- `title` string

### webhook

- `bearerToken` string (secret): The bearer token sent in the Authorization header; cannot be combined with username or password.
- `password` string (secret): The basic auth password; cannot be combined with bearerToken.
- `sendResolved` boolean: Also notify when the alert resolves. Defaults to true.
- `url` string (secret) (required): The endpoint that receives the Alertmanager webhook payload. A secret; returned by create, update and fetch by ID, never by list.
- `username` string: The basic auth user; cannot be combined with bearerToken.

## Create a channel with signoz_create_notification_channel

### 1. email — Email recipients

Sends to a comma-separated recipient list. SMTP transport comes from the deployment's global config, so only recipients and body are configurable.

```json
{
  "config": {
    "kind": "email",
    "spec": {
      "headers": {
        "Subject": "[SigNoz] {{ .CommonLabels.alertname }}"
      },
      "to": "oncall@example.com, platform@example.com"
    }
  },
  "displayName": "Email on-call",
  "name": "email-oncall"
}
```

### 2. googlechat — Google Chat space webhook

Posts to a Google Chat space through its webhook URL.

```json
{
  "config": {
    "kind": "googlechat",
    "spec": {
      "webhookUrl": "https://chat.googleapis.com/v1/spaces/AAAA0000000/messages?key=XXXX&token=XXXX"
    }
  },
  "displayName": "Google Chat platform",
  "name": "googlechat-platform"
}
```

### 3. incidentio — incident.io HTTP alert source

Sends alert events to an incident.io HTTP alert source. `url` and `token` are copied from the source's setup page; the token goes without the Bearer prefix.

```json
{
  "config": {
    "kind": "incidentio",
    "spec": {
      "metadata": {
        "team": "platform"
      },
      "token": "XXXXXXXXXXXXXXXXXXXXXXXX",
      "url": "https://api.incident.io/v2/alert_events/http/00000000-0000-0000-0000-000000000000"
    }
  },
  "displayName": "incident.io platform",
  "name": "incidentio-platform"
}
```

### 4. jira — Jira Cloud issues

Creates one Jira Cloud issue per alert group and reopens it if the alert fires again within `reopenDuration`. Authenticates with an Atlassian account email and API token.

```json
{
  "config": {
    "kind": "jira",
    "spec": {
      "apiToken": "XXXXXXXXXXXXXXXXXXXXXXXX",
      "email": "oncall@example.com",
      "issueType": "Bug",
      "labels": [
        "signoz",
        "alert"
      ],
      "project": "OPS",
      "reopenDuration": "3d",
      "site": "https://example.atlassian.net"
    }
  },
  "displayName": "Jira platform",
  "name": "jira-platform"
}
```

### 5. jsmops — Jira Service Management Ops

Creates JSM Ops alerts through an integration API key. JSM Ops speaks the Opsgenie alert API, so `priority` takes P1 to P5.

```json
{
  "config": {
    "kind": "jsmops",
    "spec": {
      "apiKey": "00000000-0000-0000-0000-000000000000",
      "priority": "P1",
      "tags": "signoz,platform"
    }
  },
  "displayName": "JSM Ops on-call",
  "name": "jsmops-oncall"
}
```

### 6. msteams — Microsoft Teams incoming webhook

Posts an adaptive card to a Teams channel through an incoming webhook.

```json
{
  "config": {
    "kind": "msteams",
    "spec": {
      "webhookUrl": "https://example.webhook.office.com/webhookb2/00000000-0000-0000-0000-000000000000"
    }
  },
  "displayName": "Teams platform",
  "name": "msteams-platform"
}
```

### 7. opsgenie — Opsgenie API integration

Creates Opsgenie alerts through an API integration key, pinned to priority P2.

```json
{
  "config": {
    "kind": "opsgenie",
    "spec": {
      "apiKey": "00000000-0000-0000-0000-000000000000",
      "priority": "P2"
    }
  },
  "displayName": "Opsgenie SRE",
  "name": "opsgenie-sre"
}
```

### 8. pagerduty — PagerDuty Events API v2

Creates PagerDuty incidents through an Events API v2 integration key.

```json
{
  "config": {
    "kind": "pagerduty",
    "spec": {
      "routingKey": "R0000000000000000000000000000000",
      "severity": "critical"
    }
  },
  "displayName": "PagerDuty on-call",
  "name": "pagerduty-oncall"
}
```

### 9. slack — Slack incoming webhook

Posts to a Slack channel through an incoming webhook. `sendResolved` is off by default for Slack, so it is turned on here.

```json
{
  "config": {
    "kind": "slack",
    "spec": {
      "apiUrl": "https://hooks.slack.com/services/T000/B000/XXXXXXXX",
      "channel": "#alerts-platform",
      "sendResolved": true
    }
  },
  "displayName": "Slack platform",
  "name": "slack-platform"
}
```

### 10. webhook_bearer — Webhook with a bearer token

Delivers the Alertmanager webhook payload to an HTTP endpoint. Use either `bearerToken` or `username`/`password`, never both.

```json
{
  "config": {
    "kind": "webhook",
    "spec": {
      "bearerToken": "00000000-0000-0000-0000-000000000000",
      "url": "https://alerts.example.com/signoz"
    }
  },
  "displayName": "Incident bot webhook",
  "name": "webhook-incident-bot"
}
```

## Replace a channel's config with signoz_update_notification_channel

### 1. slack_rotate_webhook — Replace a Slack webhook URL

Replaces the stored configuration in full; every field not sent falls back to its default.

```json
{
  "config": {
    "kind": "slack",
    "spec": {
      "apiUrl": "https://hooks.slack.com/services/T000/B000/XXXXXXXX",
      "channel": "#alerts-platform",
      "sendResolved": true
    }
  }
}
```

### 2. switch_to_pagerduty — Change the kind to PagerDuty

The kind may change on update, replacing the channel's notifier configuration while its name and references stay.

```json
{
  "config": {
    "kind": "pagerduty",
    "spec": {
      "routingKey": "R0000000000000000000000000000000",
      "severity": "critical"
    }
  }
}
```
