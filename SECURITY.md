# Security policy

## Reporting

Please do **not** open a public issue for a suspected vulnerability.
Use the repository's private security advisory feature, or contact the
maintainers through the private channel listed in the repository profile.

Include:

- affected version/commit;
- reproduction steps;
- impact and any proof-of-concept;
- whether credentials or customer data may be involved.

We will acknowledge receipt and coordinate disclosure after a fix is ready.

## Secrets and deployment

Never commit:

- `.env`, database URLs/passwords, JWT secrets;
- WeChat Pay certificates/API v3 keys;
- Resend/SMTP or model Provider keys;
- customer projects, orders, generated media, exports or production logs.

If a secret is exposed, rotate it immediately and remove it from Git history.
The public snapshot must not contain production data or deployment credentials.

## Scope

Payment, generation queues and settlement consistency are security-sensitive.
Changes to those paths should include regression tests and a review of
idempotency, authorization, amount verification and failure recovery.
