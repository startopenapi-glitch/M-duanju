# Security Policy

## Reporting a vulnerability

Please **do not** open a public GitHub/Gitee issue for security problems.

Report privately to the maintainers with:

- affected version or commit;
- a minimal reproduction;
- impact assessment;
- any suggested fix.

Do not include real user data, production credentials, payment certificates or
database dumps. Use synthetic values and redact logs.

We will acknowledge the report, investigate, and coordinate disclosure after a
fix is available.

## Supported versions

Security fixes are applied to the latest release on the default branch.

## Operational guidance

- Keep `.env`, `*_FILE` secrets, payment private keys and platform certificates
  outside the repository.
- Use HTTPS and a reverse proxy in production.
- Rotate `JWT_SECRET`, upstream API keys and payment credentials after any
  suspected exposure.
- Treat uploaded/generated media as untrusted input to ffmpeg and enforce
  timeouts, size limits and process isolation.
- Back up PostgreSQL and storage separately; test restore procedures.
