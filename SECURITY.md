# Security Policy

DNS-Go is infrastructure software that handles DNS requests, management APIs, synchronization paths, health checks, and filtering pipelines. Security and availability issues in these areas can affect production networks directly.

## Supported versions

DNS-Go is currently in an early development stage. Security fixes are applied to the `main` branch first until stable releases are established.

## Reporting a vulnerability

If you find a security issue, please avoid publishing exploit details before a fix or mitigation is available.

Preferred reporting path:

1. Open a GitHub issue with minimal public details if the issue is low risk.
2. For sensitive issues, contact the maintainer privately through the GitHub profile before disclosing details publicly.

Useful details to include:

- Affected component: DNS handler, cache, GSLB, sync API, admin API, adblock pipeline, or deployment config
- Reproduction steps or proof of concept
- Expected and actual behavior
- Possible impact on availability, routing integrity, or internal network exposure

## Security focus areas

- Malformed DNS packet handling
- DNS cache correctness and poisoning resistance
- Admin API and sync API input validation
- SSRF-style risks in health checks and upstream configuration
- Race conditions in health status, cache updates, and sync state
- Dependency vulnerabilities
- Safe production deployment with TLS, mTLS, auth gateway, and ACLs
