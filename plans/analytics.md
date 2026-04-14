# Analytics Integration Plan — Extract & Replicate from SigNoz

## Context

SigNoz uses a clean, provider-based analytics system that sends events to **Segment** (the only backend — no Mixpanel integration exists). The architecture is well-abstracted behind interfaces, making it straightforward to extract and replicate in another Go repo. This document captures every pattern, struct, function, and wiring detail needed to reproduce the analytics foundation in a new codebase.

---

## 1. Architecture Overview

```
┌─────────────────────────────────────────────────────┐
│                   Your Application                   │
│                                                      │
│  ┌──────────┐  ┌──────────┐  ┌──────────────────┐   │
│  │ Module A  │  │ Module B │  │ HTTP Handler     │   │
│  │ (e.g.    │  │ (e.g.    │  │ (e.g. /api/v1/   │   │
│  │  users)  │  │ dashbrd) │  │      event)      │   │
│  └────┬─────┘  └────┬─────┘  └───────┬──────────┘   │
│       │              │                │              │
│       ▼              ▼                ▼              │
│  ┌──────────────────────────────────────────────┐    │
│  │          analytics.Analytics interface        │    │
│  │  TrackUser · TrackGroup · IdentifyUser ·     │    │
│  │  IdentifyGroup · Send                        │    │
│  └────────────────────┬─────────────────────────┘    │
│                       │                              │
│            ┌──────────┴──────────┐                   │
│            ▼                     ▼                   │
│  ┌──────────────────┐  ┌─────────────────┐           │
│  │ segmentanalytics │  │ noopanalytics   │           │
│  │ (real Segment)   │  │ (disabled)      │           │
│  └──────────────────┘  └─────────────────┘           │
│                                                      │
│  ┌──────────────────────────────────────────────┐    │
│  │        StatsReporter (periodic ticker)        │    │
│  │  Collects stats → IdentifyGroup + TrackGroup │    │
│  │  Runs every N hours (default: 6h)            │    │
│  └──────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────┘
```

**Key design principles:**
- Interface-driven: all modules depend on the `Analytics` interface, never the Segment SDK directly
- Provider pattern: "segment" or "noop" chosen at startup based on config
- Group-aware: every event is tied to both a user and an organization (group)
- Periodic stats: a background ticker reports system-level stats on a schedule

---

## 2. Go Dependency

```
go get github.com/segmentio/analytics-go/v3@v3.2.1
```

This is the only external analytics dependency needed.

---

## 3. Core Types — `pkg/types/analyticstypes/`

Create `pkg/types/analyticstypes/message.go`:

```go
package analyticstypes

import (
    "strings"
    segment "github.com/segmentio/analytics-go/v3"
)

const (
    KeyGroupID string = "groupId"
)

// Type aliases to the Segment SDK — keeps the rest of the codebase
// decoupled from the specific analytics vendor.
type Message    = segment.Message
type Group      = segment.Group
type Identify   = segment.Identify
type Track      = segment.Track
type Traits     = segment.Traits
type Properties = segment.Properties
type Context    = segment.Context

func NewTraits() Traits {
    return segment.NewTraits()
}

func NewProperties() Properties {
    return segment.NewProperties()
}

// NewPropertiesFromMap converts a map to Segment Properties.
// Dots in keys are replaced with underscores (Segment limitation).
func NewPropertiesFromMap(m map[string]any) Properties {
    properties := NewProperties()
    for k, v := range m {
        properties.Set(strings.ReplaceAll(k, ".", "_"), v)
    }
    return properties
}

// NewTraitsFromMap converts a map to Segment Traits.
// Dots in keys are replaced with underscores.
func NewTraitsFromMap(m map[string]any) Traits {
    traits := NewTraits()
    for k, v := range m {
        traits.Set(strings.ReplaceAll(k, ".", "_"), v)
    }
    return traits
}
```

**Important detail:** Segment does not support dots in property/trait keys. The `strings.ReplaceAll(k, ".", "_")` conversion is critical.

---

## 4. Analytics Interface — `pkg/analytics/analytics.go`

```go
package analytics

import (
    "context"
    "<your-module>/pkg/types/analyticstypes"
)

type Analytics interface {
    // Start blocks until the analytics backend is stopped.
    Start(context.Context) error
    // Stop flushes pending events and closes the client.
    Stop(context.Context) error

    // Send sends raw analytics messages.
    Send(context.Context, ...analyticstypes.Message)

    // TrackGroup tracks an event at the organization/group level.
    // The userId is automatically set to "stats_<group>".
    // Parameters: group (org ID), event name, properties map.
    TrackGroup(context.Context, string, string, map[string]any)

    // TrackUser tracks an event attributed to a specific user within a group.
    // Parameters: group (org ID), user (user ID), event name, properties map.
    TrackUser(context.Context, string, string, string, map[string]any)

    // IdentifyGroup sets traits on an organization/group.
    // Parameters: group (org ID), traits map.
    IdentifyGroup(context.Context, string, map[string]any)

    // IdentifyUser sets traits on a user and associates them with a group.
    // Parameters: group (org ID), user (user ID), traits map.
    IdentifyUser(context.Context, string, string, map[string]any)
}
```

---

## 5. Configuration — `pkg/analytics/config.go`

```go
package analytics

type Config struct {
    Enabled bool   `mapstructure:"enabled"` // env: ANALYTICS_ENABLED
    Segment SegmentConfig `mapstructure:"segment"`
}

type SegmentConfig struct {
    Key string `mapstructure:"key"` // env: ANALYTICS_SEGMENT_KEY (or set via ldflags)
}

// Provider returns the provider name based on the enabled flag.
func (c Config) Provider() string {
    if c.Enabled {
        return "segment"
    }
    return "noop"
}
```

### How the Segment API key is passed (3 mechanisms, in priority order)

**1. Deprecated env var (highest priority, runs last — overwrites everything):**
`SIGNOZ_SAAS_SEGMENT_KEY` is handled in `pkg/signoz/config.go:278-281` after koanf config loading. It directly sets `config.Analytics.Segment.Key` with a deprecation warning.

**2. Environment variable (primary, recommended):**
`SIGNOZ_ANALYTICS_SEGMENT_KEY` — picked up by the koanf env provider (`pkg/config/envprovider/provider.go`).
The env provider strips the `SIGNOZ_` prefix, converts single `_` to `.` (double `__` becomes literal `_`), so:
`SIGNOZ_ANALYTICS_SEGMENT_KEY` → config path `analytics.segment.key` → `Config.Analytics.Segment.Key`

**3. Build-time ldflags (lowest priority, default value):**
The `var key string = "<unset>"` in `pkg/analytics/config.go` is used in `newConfig()` as the default. Override at build time:
```
go build -ldflags "-X <module>/pkg/analytics.key=YOUR_SEGMENT_WRITE_KEY"
```
Note: SigNoz's current Makefile does NOT set this — the key comes from env vars in practice.

**For the target repo:** Use environment variables (mechanism #2). Set `YOURPREFIX_ANALYTICS_SEGMENT_KEY` if using the same koanf env provider pattern, or just read `ANALYTICS_SEGMENT_KEY` directly from `os.Getenv()` for simplicity.

---

## 6. Segment Provider — `pkg/analytics/segmentanalytics/provider.go`

This is the real implementation. Key behaviors:

### 6.1 Initialization
```go
client, err := segment.NewWithConfig(config.Segment.Key, segment.Config{
    Logger: yourLogger, // optional
})
```

### 6.2 TrackGroup — Organization-level events
```go
func (p *provider) TrackGroup(ctx context.Context, group, event string, properties map[string]any) {
    if properties == nil { return } // nil-guard, skip if no properties

    p.client.Enqueue(analyticstypes.Track{
        UserId:     "stats_" + group,           // synthetic user for org-level tracking
        Event:      event,
        Properties: analyticstypes.NewPropertiesFromMap(properties),
        Context: &analyticstypes.Context{
            Extra: map[string]interface{}{
                analyticstypes.KeyGroupID: group, // associates event with the org
            },
        },
    })
}
```

**Critical pattern — `"stats_" + orgID`:** Organization-level events use a synthetic user ID prefixed with `stats_`. This keeps org events separate from individual user activity in Segment.

### 6.3 TrackUser — User-level events
```go
func (p *provider) TrackUser(ctx context.Context, group, user, event string, properties map[string]any) {
    if properties == nil { return }

    p.client.Enqueue(analyticstypes.Track{
        UserId:     user,                       // actual user ID
        Event:      event,
        Properties: analyticstypes.NewPropertiesFromMap(properties),
        Context: &analyticstypes.Context{
            Extra: map[string]interface{}{
                analyticstypes.KeyGroupID: group, // associates user event with their org
            },
        },
    })
}
```

### 6.4 IdentifyGroup — Set org traits (sends 2 messages)
```go
func (p *provider) IdentifyGroup(ctx context.Context, group string, traits map[string]any) {
    if traits == nil { return }

    // 1. Identify the synthetic stats user with org traits
    p.client.Enqueue(analyticstypes.Identify{
        UserId: "stats_" + group,
        Traits: analyticstypes.NewTraitsFromMap(traits),
    })

    // 2. Associate stats user with the group (org)
    p.client.Enqueue(analyticstypes.Group{
        UserId:  "stats_" + group,
        GroupId: group,
        Traits:  analyticstypes.NewTraitsFromMap(traits),
    })
}
```

### 6.5 IdentifyUser — Set user traits + associate with org (sends 2 messages)
```go
func (p *provider) IdentifyUser(ctx context.Context, group, user string, traits map[string]any) {
    if traits == nil { return }

    // 1. Identify the actual user with their traits
    p.client.Enqueue(analyticstypes.Identify{
        UserId: user,
        Traits: analyticstypes.NewTraitsFromMap(traits),
    })

    // 2. Associate the user with their org/group
    p.client.Enqueue(analyticstypes.Group{
        UserId:  user,
        GroupId: group,
        Traits:  analyticstypes.NewTraits().Set("id", group), // minimal trait required
    })
}
```

### 6.6 Lifecycle
- **Start:** Blocks on `<-stopC` channel (keeps the service alive in the registry)
- **Stop:** Calls `client.Close()` to flush queued events, then closes stopC

---

## 7. No-op Provider — `pkg/analytics/noopanalytics/provider.go`

Every method is empty. Used when `Enabled: false`. Important for:
- Local development (no events sent)
- Testing (no side effects)
- Self-hosted deployments that opt out of analytics

```go
type provider struct {
    stopC chan struct{}
}

func (p *provider) Start(_ context.Context) error { <-p.stopC; return nil }
func (p *provider) Stop(_ context.Context) error  { close(p.stopC); return nil }
func (p *provider) Send(ctx context.Context, messages ...analyticstypes.Message) {}
func (p *provider) TrackGroup(ctx context.Context, group, event string, attrs map[string]any) {}
func (p *provider) TrackUser(ctx context.Context, group, user, event string, attrs map[string]any) {}
func (p *provider) IdentifyGroup(ctx context.Context, group string, traits map[string]any) {}
func (p *provider) IdentifyUser(ctx context.Context, group, user string, traits map[string]any) {}
```

---

## 8. Provider Wiring at Startup

### 8.1 Provider factory registration
```go
func NewAnalyticsProviderFactories() NamedMap[ProviderFactory[analytics.Analytics, analytics.Config]] {
    return MustNewNamedMap(
        noopanalytics.NewFactory(),
        segmentanalytics.NewFactory(),
    )
}
```

### 8.2 Initialization at application startup
```go
analyticsInstance, err := factory.NewProviderFromNamedMap(
    ctx,
    providerSettings,
    config.Analytics,
    NewAnalyticsProviderFactories(),
    config.Analytics.Provider(), // returns "segment" or "noop"
)
```

### 8.3 Injection into modules
Modules receive `analytics.Analytics` as a constructor parameter:
```go
func NewModule(store Store, analytics analytics.Analytics, ...) Module {
    return &module{
        analytics: analytics,
        ...
    }
}
```

### 8.4 Simplified wiring (without the factory framework)
If your repo doesn't use the same factory pattern, you can simplify:
```go
func NewAnalytics(config analytics.Config) (analytics.Analytics, error) {
    if !config.Enabled {
        return noopanalytics.New(context.Background(), nil, config)
    }
    return segmentanalytics.New(context.Background(), nil, config)
}
```

---

## 9. Periodic Stats Reporter — `pkg/statsreporter/`

### 9.1 Interface
```go
type StatsReporter interface {
    Start(context.Context) error
    Stop(context.Context) error
    Report(context.Context) error
}

type StatsCollector interface {
    Collect(ctx context.Context, orgID uuid.UUID) (map[string]any, error)
}
```

### 9.2 Config
```go
type Config struct {
    Enabled  bool          `mapstructure:"enabled"`    // default: true
    Interval time.Duration `mapstructure:"interval"`   // default: 6 hours
    Collect  Collect       `mapstructure:"collect"`
}

type Collect struct {
    Identities bool `mapstructure:"identities"` // default: true
}
```

### 9.3 Report cycle (every 6 hours by default)
1. Start a `time.Ticker` at the configured interval
2. On each tick, for each organization:
   a. Run all registered `StatsCollector.Collect()` in parallel (using `sync.WaitGroup`)
   b. Merge all stats into a single `map[string]any`
   c. Add build/deployment metadata (version, branch, OS, arch, etc.)
   d. Add org metadata (name, display_name, created_at)
   e. Call `analytics.IdentifyGroup(orgID, stats)`
   f. Call `analytics.TrackGroup(orgID, "Stats Reported", stats)`
   g. If `Collect.Identities` is enabled, for each user in the org:
      - Call `analytics.IdentifyUser(orgID, userID, userTraits)`
3. On Stop: run one final Report, then stop the analytics client

### 9.4 StatsCollector pattern
Each module that has stats to report implements:
```go
func (m *module) Collect(ctx context.Context, orgID uuid.UUID) (map[string]any, error) {
    // Query your data store for counts, etc.
    return map[string]any{
        "dashboard.count": 42,
        "dashboard.panels.count": 128,
    }, nil
}
```

Collectors are registered at startup:
```go
statsCollectors := []statsreporter.StatsCollector{
    dashboardModule,
    userModule,
    alertModule,
    // ...
}
```

---

## 10. Event Tracking Patterns in Modules

### 10.1 Track on entity lifecycle (most common)
```go
// After creating an entity:
traits := map[string]any{
    "name":       user.DisplayName,
    "email":      user.Email,
    "created_at": user.CreatedAt,
}
module.analytics.IdentifyUser(ctx, orgID.String(), userID.String(), traits)
module.analytics.TrackUser(ctx, orgID.String(), userID.String(), "User Created", traits)

// After updating:
module.analytics.TrackUser(ctx, orgID.String(), userID.String(), "User Updated", traits)

// After deleting:
module.analytics.TrackUser(ctx, orgID.String(), userID.String(), "User Deleted", map[string]any{...})
```

### 10.2 Track on action
```go
module.analytics.TrackUser(ctx, orgID.String(), userID.String(), "Invite Sent", map[string]any{
    "invitee_email": invitee.Email,
    "invitee_role":  invitee.Role,
})
```

### 10.3 Track from HTTP handler (generic endpoint)
SigNoz exposes a `POST /api/v1/event` endpoint that accepts:
```go
type RegisterEventParams struct {
    EventType   string                 `json:"eventType"`   // "track", "identify", "group"
    EventName   string                 `json:"eventName"`
    Attributes  map[string]interface{} `json:"attributes"`
    RateLimited bool                   `json:"rateLimited"`
}
```

The handler extracts the authenticated user's orgID and userID from JWT claims and calls:
```go
analytics.TrackUser(ctx, claims.OrgID, claims.IdentityID(), request.EventName, request.Attributes)
```

This allows the frontend to send custom events without needing direct access to Segment.

---

## 11. Frontend Event Tracking (TypeScript/React)

### 11.1 Backend relay function — `logEvent()`
```typescript
const logEvent = async (
    eventName: string,
    attributes: Record<string, unknown>,
    eventType?: 'track' | 'group' | 'identify',
    rateLimited?: boolean,
): Promise<SuccessResponse | ErrorResponse> => {
    const { hostname } = window.location;
    const userEmail = getLocalStorageApi('LOGGED_IN_USER_EMAIL');
    const updatedAttributes = {
        ...attributes,
        deployment_url: hostname,
        user_email: userEmail,
    };
    return axios.post('/event', {
        eventName,
        attributes: updatedAttributes,
        eventType: eventType || 'track',
        rateLimited: rateLimited || false,
    });
};
```

### 11.2 Usage in components
```typescript
logEvent('Dashboard Created', { dashboard_id: id, panel_count: panels.length });
logEvent('User Activated', { email }, 'identify');
```

### 11.3 PostHog (separate from Segment, direct frontend tracking)
SigNoz also uses PostHog directly on the frontend for product analytics:
```typescript
posthog.init(process.env.POSTHOG_KEY, {
    api_host: 'https://us.i.posthog.com',
    person_profiles: 'identified_only',
});

posthog.identify(userId, { email, name, orgName, ... });
posthog.group('company', orgId, { name: orgName, ... });
```

This is a **separate** integration — not related to the Segment backend analytics. Include only if you want direct frontend analytics in addition to the backend relay.

---

## 12. Event Naming Conventions

| Event Name | Level | When | Key Properties |
|---|---|---|---|
| `Stats Reported` | Group | Every 6h (periodic) | build.*, deployment.*, telemetry.*.count |
| `User Created` | User | On user creation | name, email, created_at |
| `User Updated` | User | On user update | changed fields |
| `User Deleted` | User | On user deletion | user traits |
| `User Activated` | User | On user activation | user traits |
| `Invite Sent` | User | On invite | invitee_email, invitee_role |
| `Dashboard Created` | User | On dashboard creation | dashboard.count, panels.count |
| `Service Account Created` | User | On SA creation | SA traits |

---

## 13. Implementation Checklist for Target Repo

### Step 1: Add Segment dependency
```bash
go get github.com/segmentio/analytics-go/v3@v3.2.1
```

### Step 2: Create analytics types package
- `pkg/types/analyticstypes/message.go` — type aliases + helper functions (Section 3)

### Step 3: Create analytics interface
- `pkg/analytics/analytics.go` — interface definition (Section 4)
- `pkg/analytics/config.go` — Config struct (Section 5)

### Step 4: Create provider implementations
- `pkg/analytics/segmentanalytics/provider.go` — Segment provider (Section 6)
- `pkg/analytics/noopanalytics/provider.go` — No-op provider (Section 7)

### Step 5: Wire analytics at startup
- Create analytics instance based on config (Section 8)
- Pass as dependency to modules that need to track events

### Step 6: Add event tracking to modules
- Follow patterns in Section 10 — inject `analytics.Analytics`, call `TrackUser`/`IdentifyUser` at lifecycle points

### Step 7: (Optional) Add generic event endpoint
- `POST /api/v1/event` handler (Section 10.3) for frontend-originated events

### Step 8: (Optional) Add periodic stats reporter
- `pkg/statsreporter/` — interface, config, and analytics stats reporter (Section 9)
- Register `StatsCollector` implementations from modules
- Start reporter as a background goroutine

### Step 9: (Optional) Add frontend event tracking
- `logEvent()` function (Section 11) that relays events to backend endpoint

---

## 14. Key Design Decisions to Be Aware Of

1. **Dots → underscores in keys:** All property/trait keys have `.` replaced with `_` before sending to Segment. Your property naming should account for this.

2. **`stats_<orgID>` synthetic user:** Organization-level events use this prefix to avoid polluting real user profiles. This is a deliberate choice that affects how you query data in downstream tools.

3. **Nil-guard on properties:** All tracking methods return early if `properties` or `traits` are nil. This prevents sending empty events to Segment.

4. **IdentifyUser sends 2 Segment messages:** Both an `Identify` (set traits) and a `Group` (associate with org). This is required for Segment's group analytics to work properly.

5. **IdentifyGroup also sends 2 messages:** An `Identify` for the synthetic `stats_` user and a `Group` association.

6. **Stop flushes events:** The `Stop()` method calls `client.Close()` which flushes any queued events. The stats reporter also does a final `Report()` on shutdown to capture last-moment stats.

7. **No Mixpanel:** Despite the question, SigNoz only uses Segment. If you need Mixpanel, you can either route through Segment (which supports Mixpanel as a destination), or create a `mixpanelanalytics` provider implementing the same `Analytics` interface.

---

## 15. Missing Pieces — Additional Files

### 15.1 Segment Logger Adapter — `pkg/analytics/segmentanalytics/logger.go`

The Segment SDK requires a `segment.Logger` interface. This adapter bridges it to your app's structured logger:

```go
package segmentanalytics

import (
    "context"
    segment "github.com/segmentio/analytics-go/v3"
)

type logger struct {
    // your logger
    log *slog.Logger
}

func newSegmentLogger(log *slog.Logger) segment.Logger {
    return &logger{log: log}
}

// segment.Logger requires Logf and Errorf
func (l *logger) Logf(format string, args ...interface{}) {
    l.log.InfoContext(context.TODO(), format, args...)
}

func (l *logger) Errorf(format string, args ...interface{}) {
    l.log.ErrorContext(context.TODO(), format, args...)
}
```

This is passed to `segment.NewWithConfig(key, segment.Config{Logger: newSegmentLogger(log)})`.

### 15.2 Test Provider — `pkg/analytics/analyticstest/provider.go`

A test-only implementation with exported `Provider` struct (unlike the other providers which are unexported). Used in unit tests to avoid Segment side effects:

```go
package analyticstest

type Provider struct {
    stopC chan struct{}
}

func New() *Provider {
    return &Provider{stopC: make(chan struct{})}
}

// All methods are no-ops (same as noop provider)
// Key difference: Provider is an EXPORTED type, so tests can create it directly
// without going through the factory pattern.
```

### 15.3 No-op StatsReporter — `pkg/statsreporter/noopstatsreporter/provider.go`

Used when stats reporting is disabled. Blocks on `Start()` until `Stop()` is called. `Report()` is a no-op.

### 15.4 Event Model Types — `pkg/query-service/model/queryParams.go`

The model for the generic event endpoint:

```go
type EventType string

const (
    TrackEvent    EventType = "track"
    IdentifyEvent EventType = "identify"
    GroupEvent    EventType = "group"
)

func (e EventType) IsValid() bool {
    return e == TrackEvent || e == IdentifyEvent || e == GroupEvent
}

type RegisterEventParams struct {
    EventType   EventType              `json:"eventType"`
    EventName   string                 `json:"eventName"`
    Attributes  map[string]interface{} `json:"attributes"`
    RateLimited bool                   `json:"rateLimited"`
}
```

### 15.5 Querier Event Tracking — `pkg/querier/api.go`

Tracks query execution results. The `logEvent()` method:
- Extracts user claims from context
- Skips if no signal type (logs/traces/metrics) was used
- Skips if no referrer header is present
- Attaches query metadata as properties: `version`, `logs_used`, `traces_used`, `metrics_used`, `source`, `filter_applied`, `group_by_applied`, `query_type`, `panel_type`, `number_of_queries`
- Attaches instrumentation context comments (like code namespace, function name)
- Sends either `"Telemetry Query Returned Empty"` or `"Telemetry Query Returned Results"`

### 15.6 Important: StatsReporter creates its OWN Segment client

A subtle but important detail in `pkg/statsreporter/analyticsstatsreporter/provider.go:83`:

```go
analytics, err := segmentanalytics.New(ctx, providerSettings, analyticsConfig)
```

The StatsReporter creates a **separate** Segment analytics instance — it does NOT reuse the main application-level analytics instance. This means:
- Two Segment clients exist at runtime (main app + stats reporter)
- Each has its own event queue and flush cycle
- The stats reporter's instance is independently started/stopped

---

## 16. Complete File Tree

```
pkg/
├── analytics/
│   ├── analytics.go              # Analytics interface (5 methods + Start/Stop)
│   ├── config.go                 # Config struct, ldflags key var, provider selector
│   ├── analyticstest/
│   │   └── provider.go           # Test provider (exported type, no-op)
│   ├── noopanalytics/
│   │   └── provider.go           # No-op provider (disabled analytics)
│   └── segmentanalytics/
│       ├── provider.go           # Segment implementation (Enqueue Track/Identify/Group)
│       └── logger.go             # Segment SDK logger adapter
├── types/
│   └── analyticstypes/
│       └── message.go            # Type aliases (Track, Identify, Group, etc.) + helpers
├── statsreporter/
│   ├── statsreporter.go          # StatsReporter + StatsCollector interfaces
│   ├── config.go                 # Config (enabled, interval, collect.identities)
│   ├── analyticsstatsreporter/
│   │   └── provider.go           # Periodic reporter (ticker + parallel collectors)
│   └── noopstatsreporter/
│       └── provider.go           # No-op reporter
├── signoz/
│   ├── config.go                 # Top-level config with env var backward compat
│   ├── provider.go               # Factory registration (NewAnalyticsProviderFactories)
│   └── signoz.go                 # Startup wiring + service registry
├── query-service/
│   ├── model/
│   │   └── queryParams.go        # EventType enum + RegisterEventParams struct
│   └── app/
│       └── http_handler.go       # POST /api/v1/event handler + query tracking
└── querier/
    └── api.go                    # Query result event tracking (logEvent method)

frontend/
└── src/
    └── api/
        └── common/
            └── logEvent.ts       # Frontend event relay to backend
```

---

## 17. Source File Reference

| File (in SigNoz repo) | What to extract |
|---|---|
| `pkg/types/analyticstypes/message.go` | Type aliases, helper functions |
| `pkg/analytics/analytics.go` | Interface definition |
| `pkg/analytics/config.go` | Configuration + ldflags key var |
| `pkg/analytics/segmentanalytics/provider.go` | Segment implementation |
| `pkg/analytics/segmentanalytics/logger.go` | Segment SDK logger adapter |
| `pkg/analytics/noopanalytics/provider.go` | No-op implementation |
| `pkg/analytics/analyticstest/provider.go` | Test provider (exported, for unit tests) |
| `pkg/statsreporter/statsreporter.go` | StatsReporter + StatsCollector interfaces |
| `pkg/statsreporter/config.go` | StatsReporter config |
| `pkg/statsreporter/analyticsstatsreporter/provider.go` | Periodic reporter implementation |
| `pkg/statsreporter/noopstatsreporter/provider.go` | No-op stats reporter |
| `pkg/signoz/config.go:278-286` | Env var backward compatibility |
| `pkg/signoz/provider.go:77-82` | Factory registration pattern |
| `pkg/signoz/signoz.go:131-140` | Startup wiring |
| `pkg/config/envprovider/provider.go` | Koanf env provider (SIGNOZ_ prefix → config path) |
| `pkg/query-service/model/queryParams.go:53-70` | EventType enum + RegisterEventParams struct |
| `pkg/query-service/app/http_handler.go:1495-1510` | Generic event endpoint handler |
| `pkg/querier/api.go:238-276` | Query result event tracking |
| `pkg/modules/user/impluser/setter.go` | Example: user event tracking |
| `pkg/modules/dashboard/impldashboard/module.go` | Example: dashboard event tracking |
| `frontend/src/api/common/logEvent.ts` | Frontend event relay |

Based on above doc implement analytics in this repo following pieces from https://github.com/SigNoz/signoz. You need to record below attributes whereever possible:
1. MCP registered
2. MCP un-registerd
3. Tenant URL
4. Session ID
5. Tool call
6. Use the same semantics for the above attributes as used by traces and logs
7. At the end I should be able to ask the below questions to an analytics platform
    1. Daily tenant count
    2. Daily session count
    3. Weekly active tenant count
    4. Avg Session count per tenant
    5. Avg tool calls per session
    6. Avg tool calls per tenant
8. Verify the events and attributes sent using console log and iterate till you do it properly
9. Remove the console logs from step 8 post verification