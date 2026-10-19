# Security scan

This repository was rebuilt as a clean initial commit. The private development
history, deployment notes, internal review reports and local test evidence were
not imported.

## Final scan

- Tool: gitleaks 8.30.1
- Scope: complete Git history (`--all`) and the current tree
- Result: **0 findings**
- Also checked: no tracked `.env`, private keys, certificates, database dumps,
  database backups, payment samples or user data
- ffmpeg/ffprobe are not tracked and are not installed by the default container
  image; see `LICENSE-NOTES.md`

Reproduce:

```bash
gitleaks git . --log-opts="--all" --redact
gitleaks dir . --redact
```

## Known non-secret test fixtures

Synthetic test values and SHA256 fixture hashes were removed or explicitly
allowlisted with `gitleaks:allow` comments during the open-source review.

## Reporting

See [SECURITY.md](SECURITY.md).
