# Security Policy

## Reporting a Vulnerability

Please report security vulnerabilities privately through GitHub's
[private vulnerability reporting](https://github.com/b1rd33/tgctl-go/security/advisories/new).
Do not open a public issue for a suspected vulnerability.

GitHub Security Advisories let maintainers discuss the report privately and
coordinate a fix before disclosure. Include the affected version, impact, and a
minimal reproduction when it is safe to do so.

## Protect Telegram Data

Never include real Telegram data in a report, issue, pull request, test fixture,
or CI log. Redact at least:

- phone numbers, usernames, user/chat/channel IDs, and message contents
- API credentials, access hashes, session files, SQLite caches, and audit logs
- invite links, local filesystem paths, and downloaded media

Use synthetic placeholders in reproductions. If a secret or session artifact was
published, rotate or revoke it through the appropriate Telegram account controls;
deleting it from the latest commit does not remove it from Git history.

## Supported Versions

Security fixes are made against the current `main` branch. Maintainers may ask
reporters to confirm whether the latest release is affected.
