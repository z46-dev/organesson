# Authentication

- First database initialization creates the system-managed `organesson` local provider and pending `administrator@organesson` account. A random, single-use, 30-minute password setup token is printed once; only its SHA-256 hash is stored.
- Passwords are stored as Argon2id hashes. Expired or lost administrator links can be replaced with `organesson --config config.toml bootstrap reset-administrator-password`.
- Browser authentication uses Fiber's server-side session and CSRF middleware, with HTTP-only, SameSite cookies and login rate limiting. Sessions are in memory for this first slice and are cleared when the server restarts.
- An activated account can issue a named, expiring API bearer token from its authenticated browser session. The raw token is shown once; the database stores only its SHA-256 hash and a lookup prefix. Tokens can be revoked by their owner, and provider requests use the bearer token rather than browser cookies or CSRF.
- Every successful login resolves to a local account before permissions are checked. The built-in `organesson` realm uses local passwords; administrators can add one or more LDAP realms from Administration → Authentication. LDAP identities are linked to local accounts by a stable directory subject, with a qualified login such as `egp1042@cyber`.
- LDAP connections require TLS (`ldaps://` or LDAP with StartTLS). The optional CA field adds a trusted certificate; otherwise the system trust store is used. Service-bind passwords are encrypted in the database with AES-GCM.
- Set `ORGANESSON_AUTH_ENCRYPTION_KEY` to a persistent, base64-encoded 32-byte key before configuring LDAP. Generate a key once with `openssl rand -base64 32`, store it in the deployment's secret manager, and back it up securely. Do not regenerate it on restart: existing LDAP bind credentials cannot be decrypted without the original key.
- The Users section shows local accounts and their identity sources. LDAP users are created on first successful login; promote an LDAP account to platform administrator there before disabling the local realm. Organesson refuses to disable the local realm unless an enabled LDAP realm already has an active platform administrator, and prevents demoting the last active platform administrator.
- Alice/Bob/Charlie/Dave development identities are created only by `development seed-test-users` when `development.enable_test_fixtures = true` is explicitly set in local configuration. The command prints one-time activation tokens and is not exposed over HTTP.

## Reused modules

- `github.com/gofiber/fiber/v3/middleware/session`, `csrf`, `limiter`, and `helmet` provide the HTTP session and security middleware.
- `golang.org/x/crypto/argon2` provides Argon2id password hashing.
- `github.com/go-ldap/ldap/v3` provides LDAPv3 connections, StartTLS, and directory searches.
