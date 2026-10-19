# Contributing

Thanks for improving Drama Platform.

## Before you start

- Read `README.md` and `SECURITY.md`.
- Open an issue for significant behavior or schema changes before sending a large PR.
- Never commit `.env`, API keys, certificates, database dumps, payment data, or user media.
- Use synthetic test data only. Never copy production records into tests or fixtures.

## Development setup

```bash
cp .env.example .env
# edit .env; use local-only credentials
cd backend && go test ./...
cd ../frontend && npm ci && npm run typecheck && npm test && npm run build
```

## Pull requests

- Keep changes focused and include tests for behavior changes.
- Preserve the manual stage-confirmation workflow and credit/settlement invariants.
- For payment, job, lease, or migration changes, explain the failure and rollback path.
- Update the README or `.env.example` when adding a required configuration variable.
- Run the full test commands above before requesting review.

## Commit style

Use a short imperative subject, for example:

```text
Fix settlement release after cancelled assembly
Add Docker health check
```

## Security

Do not open a public issue for a vulnerability. Follow `SECURITY.md`.
