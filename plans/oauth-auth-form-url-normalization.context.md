# Feature: OAuth Auth Form URL Normalization - Context & Discussion

## Original Prompt
> auth screen should accept protocol-less URLs imo
> also it should do path stripping for things like https://tough-gecko.us.signoz.cloud/home

## Reference Links
- None

## Key Decisions & Discussion Log
### 2026-05-06 - Initial implementation
- Added a form-specific URL normalizer for the OAuth authorize screen.
- Protocol-less entries assume `https://`.
- Path, query, and fragment suffixes are stripped before credential validation and token storage.
- Strict `NormalizeSigNozURL` remains in use for non-form paths such as `X-SigNoz-URL`.

### 2026-05-06 - Review feedback
- Rejected URL userinfo so entries such as `https://tenant@evil.example/home` cannot normalize to the host after `@`.
- Rejected malformed explicit schemes such as `http:/tenant` and `https:/tenant`.
- Kept broader private-network SSRF policy out of scope because existing local/self-hosted tests and workflows rely on loopback URLs.
- Updated UI and README copy to describe protocol-less HTTPS assumption and path/query/fragment stripping.

## Open Questions
- [x] Should protocol-less auth-form input default to HTTPS? Answer: yes.
- [x] Should `/home` and similar suffixes be rejected or stripped? Answer: strip path, query, and fragment in the auth-form path.
- [x] Should this lenient behavior replace strict origin validation everywhere? Answer: no, keep it scoped to auth-form input.
- [ ] Should OAuth credential validation block private, loopback, link-local, or redirect targets? Out of scope for this change; needs a separate self-hosted-compatible security policy.
