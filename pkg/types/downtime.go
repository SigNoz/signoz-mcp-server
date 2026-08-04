package types

// Types for SigNoz planned-maintenance (downtime) schedules — the mechanism
// that mutes alert notifications during a maintenance window and makes
// signoz_list_alerts report an alert as silenced. SigNoz exposes no
// Alertmanager silences API; these schedules are the supported muting surface.
//
// Only the fields the create tool accepts are modelled here. Server-derived
// fields (id, status, kind, createdAt/By, updatedAt/By) are deliberately absent:
// they are not settable, and read paths pass the upstream object through
// untyped so unknown/new fields survive.

// DowntimeRecurrence describes a repeating maintenance window. When present,
// repeatType and duration are both required by the backend.
type DowntimeRecurrence struct {
	RepeatType string   `json:"repeatType,omitempty" jsonschema:"How often the window repeats: daily, weekly, or monthly. Must be set whenever recurrence is provided."`
	RepeatOn   []string `json:"repeatOn,omitempty" jsonschema:"Lowercase weekday names the window repeats on, e.g. ['saturday','sunday']. Applies to weekly recurrence."`
	Duration   string   `json:"duration,omitempty" jsonschema:"How long each occurrence lasts, as a Go duration string such as '4h' or '90m'. Must be set whenever recurrence is provided."`
}

// DowntimeScheduleWindow is the schedule block of a planned-maintenance entry.
type DowntimeScheduleWindow struct {
	Timezone   string              `json:"timezone,omitempty" jsonschema:"IANA timezone the window is interpreted in, e.g. 'America/New_York' or 'UTC'. Times are re-rendered by the backend in this timezone on read."`
	StartTime  string              `json:"startTime,omitempty" jsonschema:"Window start as an RFC 3339 timestamp with offset, e.g. '2026-08-04T22:00:00-04:00'. Must be earlier than endTime."`
	EndTime    string              `json:"endTime,omitempty" jsonschema:"Window end as an RFC 3339 timestamp with offset, e.g. '2026-08-05T02:00:00-04:00'. Must be later than startTime."`
	Recurrence *DowntimeRecurrence `json:"recurrence,omitempty" jsonschema:"Optional repetition rule. Omit for a one-off fixed window; when provided, repeatType and duration are required."`
}

// CreateDowntimeScheduleInput is the input schema for
// signoz_create_downtime_schedule (POST /api/v1/downtime_schedules).
type CreateDowntimeScheduleInput struct {
	Name          string                 `json:"name" jsonschema:"Human-readable name of the maintenance window, e.g. 'DB upgrade window'."`
	Description   string                 `json:"description,omitempty" jsonschema:"Optional free-text description of why the window exists."`
	Schedule      DowntimeScheduleWindow `json:"schedule" jsonschema:"The maintenance window itself: timezone, startTime, endTime, and an optional recurrence rule."`
	AlertIds      []string               `json:"alertIds,omitempty" jsonschema:"Alert-rule UUIDs to mute during the window. Omit or leave empty to mute ALL alert rules; obtain IDs from signoz_list_alert_rules."`
	Scope         string                 `json:"scope,omitempty" jsonschema:"Optional expr-lang boolean expression narrowing which alert instances are muted, e.g. \"service_name == 'checkout'\". This is expr-lang, not Alertmanager matcher syntax; the backend compiles it and rejects invalid expressions."`
	SearchContext string                 `json:"searchContext,omitempty" jsonschema:"Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses."`
}
