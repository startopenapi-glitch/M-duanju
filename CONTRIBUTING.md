# Contributing

Thanks for contributing.

1. Open an issue for significant behavior or schema changes before coding.
2. Keep changes focused; add tests for money, authorization, queue/settlement,
   deletion and Provider behavior.
3. Run:
   ```bash
   cd backend && go test ./...
   cd ../frontend && npm test && npm run typecheck && npm run build
   ```
4. Use DCO sign-off on commits:
   ```bash
   git commit -s -m "your change"
   ```
5. By contributing, you agree that your contribution is licensed under the
   Apache License 2.0.

Do not include real customer data, production credentials, screenshots of
customer projects or paid media in tests/fixtures. Use synthetic data.

Security reports: see [SECURITY.md](SECURITY.md).
