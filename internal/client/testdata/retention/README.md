# Recorded SigNoz Retention Responses

These retention-only JSON bodies were captured on 2026-08-01 from the three
read endpoints used by the SigNoz Settings UI on a live authenticated
workspace. The corresponding UI showed 3 months for metrics and 1
month each for traces and logs, matching the raw values and their normalized
2160/720/720-hour representation.

The fixtures contain no workspace identifiers, URLs, credentials, or telemetry.
The logs response intentionally omits `ttl_conditions`, matching the live v2
response when no custom log-retention rules are configured.
