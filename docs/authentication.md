# Authentication

- First database initialization creates the system-managed `organesson` local provider and pending `administrator@organesson` account. A random, single-use, 30-minute password setup token is printed once; only its SHA-256 hash is stored.
- Passwords are stored as Argon2id hashes. Expired or lost administrator links can be replaced with `organesson --config config.toml bootstrap reset-administrator-password`.
- Browser authentication uses Fiber's server-side session and CSRF middleware, with HTTP-only, SameSite cookies and login rate limiting. Sessions are in memory for this first slice and are cleared when the server restarts.
- An activated account can issue a named, expiring API bearer token from its authenticated browser session. The raw token is shown once; the database stores only its SHA-256 hash and a lookup prefix. Tokens can be revoked by their owner, and provider requests use the bearer token rather than browser cookies or CSRF.
- Every successful login resolves to a local account before permissions are checked. LDAP and OIDC provider login are future work; provider records exist in the schema but are not enabled by this slice.
- Alice/Bob/Charlie/Dave development identities are created only by `development seed-test-users` when `development.enable_test_fixtures = true` is explicitly set in local configuration. The command prints one-time activation tokens and is not exposed over HTTP.

## Reused modules

- `github.com/gofiber/fiber/v3/middleware/session`, `csrf`, `limiter`, and `helmet` provide the HTTP session and security middleware.
- `golang.org/x/crypto/argon2` provides Argon2id password hashing.
