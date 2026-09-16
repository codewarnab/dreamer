# Security policy

## Reporting a vulnerability

Do not open a public issue or pull request for a suspected vulnerability.
Report it privately through [GitHub Security Advisories](https://github.com/codewarnab/dreamer/security/advisories/new). Include the affected version or commit, reproduction steps, impact, and any suggested fix. Avoid including real credentials or unrelated private data.

We aim to acknowledge a report within 3 business days and provide a status update within 7 business days. Timelines for a fix and disclosure depend on severity and complexity. Please allow time for a fix and release before public disclosure. Good-faith research that avoids privacy violations, data destruction, service disruption, and access beyond what is needed to demonstrate the issue is welcome.

## Supported versions

Until Dreamer publishes its first stable release, security fixes are made on the latest commit on `main`. After releases begin, this table will identify supported release lines. Older commits and unreleased forks are not supported.

## Security model

The detailed threat model, trust boundaries, redaction behavior, sandbox limits, and known gaps are documented in [docs/SECURITY.md](docs/SECURITY.md). That document describes the implementation; this file explains how to report vulnerabilities.
