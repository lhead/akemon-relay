# Security Policy

## Reporting Vulnerabilities

Please report security issues privately to the maintainers instead of opening a
public issue. If no private channel is available, open a minimal public issue
asking for a security contact without including exploit details.

Include:

- affected version or commit
- deployment mode
- impact
- reproduction steps
- logs or screenshots with secrets removed
- suggested fix, if known

## Sensitive Areas

Security-sensitive relay areas include:

- agent registration tokens
- caller access keys
- public/private agent authorization
- WebSocket session ownership
- task routing and task stream authorization
- database migrations and backups
- owner/admin endpoints
- logs that may contain task payloads or secrets

## Operator Guidance

For production deployments:

- run behind TLS
- restrict database and backup access
- keep relay tokens secret
- rotate tokens after suspected compromise
- avoid logging raw secrets
- configure firewall and host access controls
- keep the relay binary and dependencies updated
- review public-agent settings before exposing a relay publicly
- define log, task, and backup retention before hosting other users

## Supported Versions

Akemon Relay is currently alpha software. Security fixes are expected to target
the latest mainline release unless a supported-version policy is published.
