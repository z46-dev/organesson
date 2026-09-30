# Authentication

- Organesson always creates the system-managed `organesson` local authentication provider and pending `administrator@organesson` account on first database initialization.
- A single-use, short-lived password-set link is printed once to the startup console. Only its hash is stored; redeeming it activates the account and creates an Argon2id password credential.
- Expired bootstrap links are replaced only through the future host-local `organesson bootstrap reset-administrator-password` command.
- Administrators configure additional LDAP and OIDC providers in the administration UI. Provider configuration and encrypted provider secrets are stored in the database, not `config.toml`.
- Every successful authentication resolves to a local account before authorization, group membership, audit, or OpenTofu access processing.

## Planned modules

- `github.com/gofiber/fiber/v3` with session, CSRF, limiter, and security-header middleware.
- `github.com/go-ldap/ldap/v3` for direct LDAP authentication.
- `github.com/coreos/go-oidc/v3/oidc` and `golang.org/x/oauth2` for OIDC authentication.
- `golang.org/x/crypto/argon2` for local password hashing.
