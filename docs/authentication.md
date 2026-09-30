# Authentication

- First database initialization creates the system-managed `organesson` local provider and pending `administrator@organesson` account. A random, single-use, 30-minute password setup token is printed once; only its SHA-256 hash is stored.
- Passwords are stored as Argon2id hashes. Expired or lost administrator links can be replaced with `organesson --config config.toml bootstrap reset-administrator-password`.
- Browser authentication uses Fiber's server-side session and CSRF middleware, with HTTP-only, SameSite cookies and login rate limiting. Sessions are in memory for this first slice and are cleared when the server restarts.
- Every successful login resolves to a local account before permissions are checked. LDAP and OIDC provider login are future work; provider records exist in the schema but are not enabled by this slice.

## Reused modules

- `github.com/gofiber/fiber/v3/middleware/session`, `csrf`, `limiter`, and `helmet` provide the HTTP session and security middleware.
- `golang.org/x/crypto/argon2` provides Argon2id password hashing.
