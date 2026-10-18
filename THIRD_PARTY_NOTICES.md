# Third-party notices

This project depends on open-source packages. The list below covers direct
runtime dependencies; it is not a substitute for a complete license scan.

## Go

- github.com/gin-contrib/cors — MIT
- github.com/gin-gonic/gin — MIT
- github.com/golang-jwt/jwt/v5 — MIT
- github.com/google/uuid — BSD-3-Clause
- github.com/jackc/pgx/v5 — MIT
- github.com/skip2/go-qrcode — MIT
- golang.org/x/crypto — BSD-3-Clause

Indirect modules are listed in `backend/go.sum`; their licenses remain with
their respective authors.

## Frontend

- React / React DOM — MIT
- react-router-dom — MIT
- qrcode — MIT
- lucide-react — ISC
- clsx — MIT
- tailwind-merge — MIT
- Vite — MIT
- TypeScript — Apache-2.0
- Vitest — MIT
- Testing Library — MIT
- Tailwind CSS — MIT

Run a dependency license scanner before publishing a release and add any
project-specific notices required by your distribution.

## External services

The project can integrate with third-party model and payment APIs. Those
services are not distributed with this repository; their terms, credentials,
content policies and fees are the deployer's responsibility.
