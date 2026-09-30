package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/z46-dev/gosqlite"
	"github.com/z46-dev/organesson/backend/db"
	"golang.org/x/crypto/argon2"
)

const (
	passwordSaltLength = 16
	passwordHashLength = 32
	passwordMemoryKiB  = 64 * 1024
	passwordIterations = 3
	passwordThreads    = 2
	tokenLength        = 32
	tokenLifetime      = 30 * time.Minute
	minimumPasswordLen = 12
	maximumPasswordLen = 1024
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidToken       = errors.New("invalid or expired password link")
	ErrInvalidPassword    = errors.New("password must be between 12 and 1024 bytes")
	ErrForbidden          = errors.New("permission denied")
	ErrInvalidInput       = errors.New("invalid input")
	ErrConflict           = errors.New("account already exists")
)

type (
	// Service handles local password credentials and one-time password links.
	Service struct {
		store     *db.Store
		dummyHash string
		now       func() time.Time
		tokenLock sync.Mutex
	}

	// LocalAccountSetup contains a new local account and its one-time activation token.
	LocalAccountSetup struct {
		Account       *db.Account
		QualifiedName string
		SetupToken    string
	}

	// APITokenCredential contains the raw token exactly once and its safe metadata.
	APITokenCredential struct {
		Token  *db.APIToken
		Secret string
	}
)

// New creates a local authentication service with a timing equalization hash.
func New(store *db.Store) (service *Service, err error) {
	var salt [passwordSaltLength]byte
	if _, err = rand.Read(salt[:]); err != nil {
		return
	}

	service = &Service{store: store, now: time.Now}
	service.dummyHash = encodePasswordHash(argon2.IDKey([]byte("invalid-account-password"), salt[:], passwordIterations, passwordMemoryKiB, passwordThreads, passwordHashLength), salt[:])
	return
}

// EnsureInitialActivationLink creates a first-time admin link only when none remains pending.
func (service *Service) EnsureInitialActivationLink() (token string, created bool, err error) {
	service.tokenLock.Lock()
	defer service.tokenLock.Unlock()

	var account *db.Account
	if account, _, err = service.store.InitialAdministrator(); err != nil {
		return
	}
	if account.ActivatedAt != nil {
		return
	}

	var tokens []*db.PasswordResetToken
	if tokens, err = service.store.PasswordResetTokens.SelectAll(); err != nil {
		return
	}
	for _, candidate := range tokens {
		if candidate.AccountID == account.ID && candidate.Purpose == db.PasswordResetPurposeActivation && candidate.UsedAt == nil {
			return
		}
	}

	token, err = service.createPasswordLink(account.ID, db.PasswordResetPurposeActivation)
	created = err == nil
	return
}

// CreateLocalAccount provisions a non-administrator local identity for a platform administrator.
func (service *Service) CreateLocalAccount(actorID int, username string, displayName string) (setup *LocalAccountSetup, err error) {
	service.tokenLock.Lock()
	defer service.tokenLock.Unlock()

	var actor *db.Account
	if actor, err = service.store.Accounts.Select(actorID); err != nil {
		return
	}
	if actor == nil || actor.Disabled || !actor.PlatformAdministrator {
		err = ErrForbidden
		return
	}

	username = strings.ToLower(strings.TrimSpace(username))
	displayName = strings.TrimSpace(displayName)
	if !validLocalUsername(username) || displayName == "" || len(displayName) > 128 {
		err = ErrInvalidInput
		return
	}
	var qualifiedName string = username + "@organesson"
	var identities []*db.AccountIdentity
	if identities, err = service.store.AccountIdentities.SelectAll(); err != nil {
		return
	}
	for _, identity := range identities {
		if strings.EqualFold(identity.QualifiedName, qualifiedName) {
			err = ErrConflict
			return
		}
	}

	var providers []*db.AuthenticationProvider
	if providers, err = service.store.AuthenticationProviders.SelectAll(); err != nil {
		return
	}
	var localProvider *db.AuthenticationProvider
	for _, provider := range providers {
		if provider.Alias == "organesson" && provider.Kind == db.AuthenticationProviderKindLocal && provider.Enabled {
			localProvider = provider
			break
		}
	}
	if localProvider == nil {
		err = errors.New("local authentication provider is unavailable")
		return
	}

	var now time.Time = service.now()
	var account *db.Account = &db.Account{DisplayName: displayName, CreatedAt: now}
	if err = service.store.Accounts.Insert(account); err != nil {
		return
	}
	var identity *db.AccountIdentity = &db.AccountIdentity{
		AccountID:                account.ID,
		AuthenticationProviderID: localProvider.ID,
		ProviderSubject:          username,
		ProviderSubjectKey:       "organesson:" + username,
		QualifiedName:            qualifiedName,
		CreatedAt:                now,
	}
	if err = service.store.AccountIdentities.Insert(identity); err != nil {
		_ = service.store.Accounts.Delete(account.ID)
		return
	}

	var setupToken string
	if setupToken, err = service.createPasswordLink(account.ID, db.PasswordResetPurposeActivation); err != nil {
		_ = service.store.Accounts.Delete(account.ID)
		return
	}
	var detailsJSON []byte
	if detailsJSON, err = json.Marshal(map[string]string{"qualified_name": qualifiedName}); err != nil {
		_ = service.store.Accounts.Delete(account.ID)
		return
	}
	var actorIDPointer *int = &actorID
	if err = service.store.AuditEvents.Insert(&db.AuditEvent{
		ActorAccountID: actorIDPointer,
		Action:         "account.local.create",
		Target:         fmt.Sprintf("account:%d", account.ID),
		Result:         "succeeded",
		DetailsJSON:    string(detailsJSON),
		CreatedAt:      now,
	}); err != nil {
		_ = service.store.Accounts.Delete(account.ID)
		return
	}
	setup = &LocalAccountSetup{Account: account, QualifiedName: qualifiedName, SetupToken: setupToken}
	return
}

// CreateDevelopmentTestUsers creates the four documented test identities with activation links.
func (service *Service) CreateDevelopmentTestUsers(actorID int) (setups []*LocalAccountSetup, err error) {
	var identities []*db.AccountIdentity
	if identities, err = service.store.AccountIdentities.SelectAll(); err != nil {
		return
	}
	var usernames []string = []string{"alice", "bob", "charlie", "dave"}
	for _, username := range usernames {
		var exists bool
		for _, identity := range identities {
			if strings.EqualFold(identity.QualifiedName, username+"@organesson") {
				exists = true
				break
			}
		}
		if exists {
			continue
		}
		var setup *LocalAccountSetup
		if setup, err = service.CreateLocalAccount(actorID, username, strings.ToUpper(username[:1])+username[1:]); err != nil {
			return
		}
		setups = append(setups, setup)
	}
	return
}

// ResetLocalAccountLink replaces unused local activation or password-reset links.
func (service *Service) ResetLocalAccountLink(actorID int, accountID int) (token string, err error) {
	service.tokenLock.Lock()
	defer service.tokenLock.Unlock()

	var actor *db.Account
	if actor, err = service.store.Accounts.Select(actorID); err != nil {
		return
	}
	if actor == nil || actor.Disabled || !actor.PlatformAdministrator {
		err = ErrForbidden
		return
	}
	var account *db.Account
	if account, err = service.store.Accounts.Select(accountID); err != nil {
		return
	}
	if account == nil || account.Disabled {
		err = ErrInvalidToken
		return
	}
	var identities []*db.AccountIdentity
	if identities, err = service.store.AccountIdentities.SelectAll(); err != nil {
		return
	}
	var isLocalAccount bool
	for _, identity := range identities {
		if identity.AccountID != accountID {
			continue
		}
		var provider *db.AuthenticationProvider
		if provider, err = service.store.AuthenticationProviders.Select(identity.AuthenticationProviderID); err != nil {
			return
		}
		isLocalAccount = provider != nil && provider.Kind == db.AuthenticationProviderKindLocal && provider.Enabled
		break
	}
	if !isLocalAccount {
		err = ErrInvalidToken
		return
	}

	var tokens []*db.PasswordResetToken
	if tokens, err = service.store.PasswordResetTokens.SelectAll(); err != nil {
		return
	}
	var now time.Time = service.now()
	for _, candidate := range tokens {
		if candidate.AccountID == accountID && candidate.UsedAt == nil {
			candidate.UsedAt = &now
			if err = service.store.PasswordResetTokens.Update(candidate); err != nil {
				return
			}
		}
	}
	var purpose db.PasswordResetPurpose = db.PasswordResetPurposePasswordReset
	if account.ActivatedAt == nil {
		purpose = db.PasswordResetPurposeActivation
	}
	if token, err = service.createPasswordLink(accountID, purpose); err != nil {
		return
	}
	var detailsJSON []byte
	if detailsJSON, err = json.Marshal(map[string]int{"account_id": accountID}); err != nil {
		return
	}
	var actorIDPointer *int = &actorID
	err = service.store.AuditEvents.Insert(&db.AuditEvent{
		ActorAccountID: actorIDPointer,
		Action:         "account.password_link.reset",
		Target:         fmt.Sprintf("account:%d", accountID),
		Result:         "succeeded",
		DetailsJSON:    string(detailsJSON),
		CreatedAt:      now,
	})
	return
}

// ResetInitialAdministratorLink replaces pending activation links for the built-in administrator.
func (service *Service) ResetInitialAdministratorLink() (token string, err error) {
	service.tokenLock.Lock()
	defer service.tokenLock.Unlock()

	var account *db.Account
	if account, _, err = service.store.InitialAdministrator(); err != nil {
		return
	}
	if account.Disabled {
		err = errors.New("initial administrator account is disabled")
		return
	}

	var tokens []*db.PasswordResetToken
	if tokens, err = service.store.PasswordResetTokens.SelectAll(); err != nil {
		return
	}
	var now time.Time = service.now()
	for _, candidate := range tokens {
		if candidate.AccountID == account.ID && candidate.UsedAt == nil {
			candidate.UsedAt = &now
			if err = service.store.PasswordResetTokens.Update(candidate); err != nil {
				return
			}
		}
	}

	var purpose db.PasswordResetPurpose = db.PasswordResetPurposePasswordReset
	if account.ActivatedAt == nil {
		purpose = db.PasswordResetPurposeActivation
	}
	token, err = service.createPasswordLink(account.ID, purpose)
	return
}

// CreateAPIToken creates a bounded-lifetime bearer credential for an active account.
func (service *Service) CreateAPIToken(accountID int, name string, lifetime time.Duration) (credential *APITokenCredential, err error) {
	var account *db.Account
	if account, err = service.AccountByID(accountID); err != nil {
		return
	}
	if account == nil {
		err = ErrForbidden
		return
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 || lifetime < time.Hour || lifetime > 365*24*time.Hour {
		err = ErrInvalidInput
		return
	}

	var randomBytes [tokenLength]byte
	if _, err = rand.Read(randomBytes[:]); err != nil {
		return
	}
	var secret string = "orgn_" + base64.RawURLEncoding.EncodeToString(randomBytes[:])
	var tokenHash [sha256.Size]byte = sha256.Sum256([]byte(secret))
	var now time.Time = service.now()
	var expiresAt time.Time = now.Add(lifetime)
	var token *db.APIToken = &db.APIToken{
		AccountID: accountID,
		Name:      name,
		Prefix:    secret[:13],
		TokenHash: tokenHash[:],
		CreatedAt: now,
		ExpiresAt: &expiresAt,
	}
	if err = service.store.APITokens.Insert(token); err != nil {
		return
	}
	var actorIDPointer *int = &accountID
	if err = service.store.AuditEvents.Insert(&db.AuditEvent{
		ActorAccountID: actorIDPointer,
		Action:         "api_token.create",
		Target:         fmt.Sprintf("api_token:%d", token.ID),
		Result:         "succeeded",
		DetailsJSON:    "{}",
		CreatedAt:      now,
	}); err != nil {
		_ = service.store.APITokens.Delete(token.ID)
		return
	}
	credential = &APITokenCredential{Token: token, Secret: secret}
	return
}

// AuthenticateAPIToken validates an opaque bearer token and returns its current account.
func (service *Service) AuthenticateAPIToken(secret string) (account *db.Account, err error) {
	if len(secret) < 16 || len(secret) > 128 {
		err = ErrInvalidCredentials
		return
	}
	var tokens []*db.APIToken
	if tokens, err = service.store.APITokens.SelectAll(); err != nil {
		return
	}
	var suppliedHash [sha256.Size]byte = sha256.Sum256([]byte(secret))
	var matched *db.APIToken
	for _, token := range tokens {
		if token.Prefix == secret[:13] && len(token.TokenHash) == len(suppliedHash) && subtle.ConstantTimeCompare(token.TokenHash, suppliedHash[:]) == 1 {
			matched = token
			break
		}
	}
	if matched == nil || matched.RevokedAt != nil || matched.ExpiresAt == nil || !matched.ExpiresAt.After(service.now()) {
		err = ErrInvalidCredentials
		return
	}
	if account, err = service.AccountByID(matched.AccountID); err != nil {
		return
	}
	if account == nil {
		err = ErrInvalidCredentials
		return
	}
	var now time.Time = service.now()
	matched.LastUsedAt = &now
	if err = service.store.APITokens.Update(matched); err != nil {
		return nil, err
	}
	return
}

// RevokeAPIToken disables one bearer credential owned by the current account.
func (service *Service) RevokeAPIToken(accountID int, tokenID int) (err error) {
	var token *db.APIToken
	if token, err = service.store.APITokens.Select(tokenID); err != nil {
		return
	}
	if token == nil || token.AccountID != accountID || token.RevokedAt != nil {
		err = ErrInvalidToken
		return
	}
	var now time.Time = service.now()
	token.RevokedAt = &now
	if err = service.store.APITokens.Update(token); err != nil {
		return
	}
	var actorIDPointer *int = &accountID
	err = service.store.AuditEvents.Insert(&db.AuditEvent{
		ActorAccountID: actorIDPointer,
		Action:         "api_token.revoke",
		Target:         fmt.Sprintf("api_token:%d", tokenID),
		Result:         "succeeded",
		DetailsJSON:    "{}",
		CreatedAt:      now,
	})
	return
}

// RedeemPasswordLink validates a one-time token and activates or resets a local password.
func (service *Service) RedeemPasswordLink(token string, password string) (account *db.Account, err error) {
	service.tokenLock.Lock()
	defer service.tokenLock.Unlock()

	if err = validatePassword(password); err != nil {
		return
	}

	var resetToken *db.PasswordResetToken
	if resetToken, err = service.findValidPasswordToken(token); err != nil {
		return
	}
	if resetToken == nil {
		err = ErrInvalidToken
		return
	}

	if account, err = service.store.Accounts.Select(resetToken.AccountID); err != nil {
		return
	}
	if account == nil || account.Disabled {
		err = ErrInvalidToken
		return
	}
	if resetToken.Purpose == db.PasswordResetPurposeActivation && account.ActivatedAt != nil {
		err = ErrInvalidToken
		return
	}
	if resetToken.Purpose == db.PasswordResetPurposePasswordReset && account.ActivatedAt == nil {
		err = ErrInvalidToken
		return
	}

	var passwordHash string
	if passwordHash, err = hashPassword(password); err != nil {
		return
	}

	var now time.Time = service.now()
	var rowsAffected int64
	if rowsAffected, err = service.store.PasswordResetTokens.UpdateWithFilter(
		gosqlite.NewFilter().
			KeyCmp(service.store.PasswordResetTokens.FieldByGoName("ID"), gosqlite.OpEqual, resetToken.ID).
			And().
			KeyCmp(service.store.PasswordResetTokens.FieldByGoName("UsedAt"), gosqlite.OpIsNull, nil).
			And().
			KeyCmp(service.store.PasswordResetTokens.FieldByGoName("ExpiresAt"), gosqlite.OpGreaterThan, now),
		gosqlite.SetField(service.store.PasswordResetTokens.FieldByGoName("UsedAt"), &now),
	); err != nil {
		return
	}
	if rowsAffected != 1 {
		err = ErrInvalidToken
		return
	}

	var credential *db.LocalCredential
	if credential, err = service.store.LocalCredentials.Select(account.ID); err != nil {
		return
	}
	var isNewCredential bool = credential == nil
	if credential == nil {
		credential = &db.LocalCredential{AccountID: account.ID}
	}
	credential.PasswordHash = passwordHash
	credential.PasswordSetAt = now
	if isNewCredential {
		if err = service.store.LocalCredentials.Insert(credential); err != nil {
			return
		}
	} else if err = service.store.LocalCredentials.Update(credential); err != nil {
		return
	}

	if account.ActivatedAt == nil {
		account.ActivatedAt = &now
		if err = service.store.Accounts.Update(account); err != nil {
			return
		}
	}
	return
}

// Authenticate verifies a local password and returns its active Organesson account.
func (service *Service) Authenticate(qualifiedName string, password string) (account *db.Account, err error) {
	if len(password) > maximumPasswordLen {
		_ = verifyPassword(service.dummyHash, "")
		err = ErrInvalidCredentials
		return
	}

	var identities []*db.AccountIdentity
	if identities, err = service.store.AccountIdentities.SelectAll(); err != nil {
		return
	}

	var identity *db.AccountIdentity
	for _, candidate := range identities {
		if strings.EqualFold(candidate.QualifiedName, strings.TrimSpace(qualifiedName)) {
			identity = candidate
			break
		}
	}
	if identity == nil {
		_ = verifyPassword(service.dummyHash, password)
		err = ErrInvalidCredentials
		return
	}

	var provider *db.AuthenticationProvider
	if provider, err = service.store.AuthenticationProviders.Select(identity.AuthenticationProviderID); err != nil {
		return
	}
	if provider == nil || provider.Kind != db.AuthenticationProviderKindLocal || !provider.Enabled {
		_ = verifyPassword(service.dummyHash, password)
		err = ErrInvalidCredentials
		return
	}

	var credential *db.LocalCredential
	if credential, err = service.store.LocalCredentials.Select(identity.AccountID); err != nil {
		return
	}
	if credential == nil || !verifyPassword(credential.PasswordHash, password) {
		if credential == nil {
			_ = verifyPassword(service.dummyHash, password)
		}
		err = ErrInvalidCredentials
		return
	}

	if account, err = service.store.Accounts.Select(identity.AccountID); err != nil {
		return
	}
	if account == nil || account.Disabled || account.ActivatedAt == nil {
		account = nil
		err = ErrInvalidCredentials
	}
	return
}

// AccountByID returns an active account for a validated server-side session.
func (service *Service) AccountByID(accountID int) (account *db.Account, err error) {
	if account, err = service.store.Accounts.Select(accountID); err != nil {
		return
	}
	if account == nil || account.Disabled || account.ActivatedAt == nil {
		account = nil
	}
	return
}

// SetupRequired reports whether the built-in administrator still needs a password.
func (service *Service) SetupRequired() (required bool, err error) {
	var account *db.Account
	if account, _, err = service.store.InitialAdministrator(); err != nil {
		return
	}
	required = account.ActivatedAt == nil && !account.Disabled
	return
}

// createPasswordLink stores a one-time token hash and returns the raw value exactly once.
func (service *Service) createPasswordLink(accountID int, purpose db.PasswordResetPurpose) (token string, err error) {
	var tokenBytes [tokenLength]byte
	if _, err = rand.Read(tokenBytes[:]); err != nil {
		return
	}
	token = base64.RawURLEncoding.EncodeToString(tokenBytes[:])
	var tokenHash [sha256.Size]byte = sha256.Sum256([]byte(token))
	err = service.store.PasswordResetTokens.Insert(&db.PasswordResetToken{
		AccountID: accountID,
		Purpose:   purpose,
		TokenHash: tokenHash[:],
		ExpiresAt: service.now().Add(tokenLifetime),
		CreatedAt: service.now(),
	})
	return
}

// findValidPasswordToken compares token hashes without exposing them to database queries.
func (service *Service) findValidPasswordToken(token string) (result *db.PasswordResetToken, err error) {
	if token == "" || len(token) > 256 {
		return
	}

	var tokenHash [sha256.Size]byte = sha256.Sum256([]byte(token))
	var tokens []*db.PasswordResetToken
	if tokens, err = service.store.PasswordResetTokens.SelectAll(); err != nil {
		return
	}
	var now time.Time = service.now()
	for _, candidate := range tokens {
		if candidate.UsedAt == nil && now.Before(candidate.ExpiresAt) && subtle.ConstantTimeCompare(candidate.TokenHash, tokenHash[:]) == 1 {
			result = candidate
			return
		}
	}
	return
}

// hashPassword encodes an Argon2id password hash with per-password random salt.
func hashPassword(password string) (encoded string, err error) {
	var salt [passwordSaltLength]byte
	if _, err = rand.Read(salt[:]); err != nil {
		return
	}
	var hash []byte = argon2.IDKey([]byte(password), salt[:], passwordIterations, passwordMemoryKiB, passwordThreads, passwordHashLength)
	encoded = encodePasswordHash(hash, salt[:])
	return
}

// encodePasswordHash serializes parameters, salt, and hash for password verification.
func encodePasswordHash(hash []byte, salt []byte) (encoded string) {
	encoded = fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", passwordMemoryKiB, passwordIterations, passwordThreads, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash))
	return
}

// verifyPassword checks the encoded Argon2id hash and rejects unreasonable stored parameters.
func verifyPassword(encoded string, password string) (valid bool) {
	var parts []string = strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return
	}

	var parameters []string = strings.Split(parts[3], ",")
	if len(parameters) != 3 {
		return
	}
	var memory, iterations, threads uint64
	var err error
	if memory, err = parseParameter(parameters[0], "m="); err != nil || memory < 8*1024 || memory > 256*1024 {
		return
	}
	if iterations, err = parseParameter(parameters[1], "t="); err != nil || iterations < 1 || iterations > 10 {
		return
	}
	if threads, err = parseParameter(parameters[2], "p="); err != nil || threads < 1 || threads > 16 {
		return
	}

	var salt, expected []byte
	if salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil || len(salt) < 8 || len(salt) > 64 {
		return
	}
	if expected, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil || len(expected) < 16 || len(expected) > 64 {
		return
	}
	var actual []byte = argon2.IDKey([]byte(password), salt, uint32(iterations), uint32(memory), uint8(threads), uint32(len(expected)))
	valid = subtle.ConstantTimeCompare(actual, expected) == 1
	return
}

// parseParameter parses one bounded Argon2 parameter.
func parseParameter(value string, prefix string) (number uint64, err error) {
	if !strings.HasPrefix(value, prefix) {
		err = errors.New("invalid password hash parameter")
		return
	}
	number, err = strconv.ParseUint(strings.TrimPrefix(value, prefix), 10, 32)
	return
}

// validatePassword enforces the local password input bounds before expensive hashing.
func validatePassword(password string) (err error) {
	if len(password) < minimumPasswordLen || len(password) > maximumPasswordLen {
		err = ErrInvalidPassword
	}
	return
}

// validLocalUsername limits local qualified names to safe, predictable ASCII identifiers.
func validLocalUsername(username string) (valid bool) {
	if len(username) < 1 || len(username) > 64 {
		return
	}
	for index, character := range []byte(username) {
		var alphaNumeric bool = character >= 'a' && character <= 'z' || character >= '0' && character <= '9'
		if !alphaNumeric && !(index > 0 && (character == '.' || character == '_' || character == '-')) {
			return
		}
	}
	valid = true
	return
}
