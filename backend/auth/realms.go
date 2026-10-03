package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/z46-dev/organesson/backend/db"
)

var (
	ldapAttributeName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-]*$`)
	realmAlias        = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
)

type (
	// LDAPRealmConfiguration contains searchable LDAP connection settings without bind credentials.
	LDAPRealmConfiguration struct {
		URL                  string `json:"url"`
		BaseDN               string `json:"base_dn"`
		UserFilter           string `json:"user_filter"`
		UsernameAttribute    string `json:"username_attribute"`
		DisplayNameAttribute string `json:"display_name_attribute"`
		EmailAttribute       string `json:"email_attribute,omitempty"`
		BindDN               string `json:"bind_dn"`
		CACertificatePEM     string `json:"ca_certificate_pem,omitempty"`
	}

	// AuthenticationRealm describes a configured login namespace without exposing secret material.
	AuthenticationRealm struct {
		Alias           string                 `json:"alias"`
		Kind            string                 `json:"kind"`
		Enabled         bool                   `json:"enabled"`
		SystemManaged   bool                   `json:"system_managed"`
		HasBindPassword bool                   `json:"has_bind_password"`
		Configuration   LDAPRealmConfiguration `json:"configuration,omitempty"`
	}

	LDAPRealmInput struct {
		Alias   string `json:"alias"`
		Enabled bool   `json:"enabled"`
		LDAPRealmConfiguration
		BindPassword string `json:"bind_password,omitempty"`
	}

	ldapBindSecret struct {
		Version    int    `json:"version"`
		Nonce      string `json:"nonce"`
		Ciphertext string `json:"ciphertext"`
	}

	ldapUser struct {
		Subject  string
		Username string
		Name     string
	}
)

// ListAuthenticationRealms returns provider settings with credentials withheld.
func (service *Service) ListAuthenticationRealms() (realms []AuthenticationRealm, err error) {
	var providers []*db.AuthenticationProvider
	if providers, err = service.store.AuthenticationProviders.SelectAll(); err != nil {
		return
	}
	for _, provider := range providers {
		var realm AuthenticationRealm = AuthenticationRealm{
			Alias: provider.Alias, Enabled: provider.Enabled, SystemManaged: provider.SystemManaged,
		}
		switch provider.Kind {
		case db.AuthenticationProviderKindLocal:
			realm.Kind = "local"
		case db.AuthenticationProviderKindLDAP:
			realm.Kind = "ldap"
			if err = json.Unmarshal([]byte(provider.ConfigurationJSON), &realm.Configuration); err != nil {
				return
			}
			var secret ldapBindSecret
			if err = json.Unmarshal([]byte(provider.EncryptedSecretsJSON), &secret); err != nil {
				return
			}
			realm.HasBindPassword = secret.Ciphertext != ""
		default:
			continue
		}
		realms = append(realms, realm)
	}
	return
}

// AuthenticationEncryptionReady reports whether this process can encrypt LDAP credentials at rest.
func (service *Service) AuthenticationEncryptionReady() (ready bool) {
	ready = len(service.encryptionKey) == 32
	return
}

// CreateLDAPRealm persists a TLS-protected LDAP provider and encrypted service-bind secret.
func (service *Service) CreateLDAPRealm(input LDAPRealmInput) (realm AuthenticationRealm, err error) {
	input.Alias = strings.ToLower(strings.TrimSpace(input.Alias))
	input.URL = strings.TrimSpace(input.URL)
	input.BaseDN = strings.TrimSpace(input.BaseDN)
	input.BindDN = strings.TrimSpace(input.BindDN)
	input.UserFilter = strings.TrimSpace(input.UserFilter)
	if err = validateLDAPRealm(input.LDAPRealmConfiguration); err != nil {
		return
	}
	if input.BindPassword == "" {
		err = errors.New("LDAP service bind password is required")
		return
	}
	if !realmAlias.MatchString(input.Alias) || input.Alias == "organesson" {
		err = fmt.Errorf("realm alias must be a lowercase name and cannot be organesson")
		return
	}
	if len(service.encryptionKey) != 32 {
		err = ErrEncryptionKeyRequired
		return
	}
	var providers []*db.AuthenticationProvider
	if providers, err = service.store.AuthenticationProviders.SelectAll(); err != nil {
		return
	}
	for _, provider := range providers {
		if strings.EqualFold(provider.Alias, input.Alias) {
			err = ErrConflict
			return
		}
	}
	var secretJSON []byte
	if secretJSON, err = service.encryptBindPassword(input.Alias, input.BindPassword); err != nil {
		return
	}
	var configurationJSON []byte
	if configurationJSON, err = json.Marshal(input.LDAPRealmConfiguration); err != nil {
		return
	}
	var provider *db.AuthenticationProvider = &db.AuthenticationProvider{
		Alias: input.Alias, Kind: db.AuthenticationProviderKindLDAP, Enabled: input.Enabled,
		ConfigurationJSON: string(configurationJSON), EncryptedSecretsJSON: string(secretJSON),
	}
	if err = service.store.AuthenticationProviders.Insert(provider); err != nil {
		return
	}
	realm = AuthenticationRealm{Alias: provider.Alias, Kind: "ldap", Enabled: provider.Enabled, HasBindPassword: true, Configuration: input.LDAPRealmConfiguration}
	return
}

// UpdateLDAPRealm changes a realm and preserves its bind password when no replacement is submitted.
func (service *Service) UpdateLDAPRealm(alias string, input LDAPRealmInput) (realm AuthenticationRealm, err error) {
	var provider *db.AuthenticationProvider
	if provider, err = service.findProvider(alias); err != nil || provider == nil || provider.Kind != db.AuthenticationProviderKindLDAP {
		if err == nil {
			err = ErrInvalidInput
		}
		return
	}
	input.URL = strings.TrimSpace(input.URL)
	input.BaseDN = strings.TrimSpace(input.BaseDN)
	input.BindDN = strings.TrimSpace(input.BindDN)
	input.UserFilter = strings.TrimSpace(input.UserFilter)
	if err = validateLDAPRealm(input.LDAPRealmConfiguration); err != nil {
		return
	}
	if !input.Enabled && provider.Enabled {
		var safeAlternateAdmin bool
		if safeAlternateAdmin, err = service.hasEnabledAdministratorIdentityExcept(provider.ID); err != nil {
			return
		}
		if !safeAlternateAdmin {
			err = errors.New("cannot disable this realm until a platform administrator has another enabled sign-in identity")
			return
		}
	}
	if input.BindPassword != "" {
		var secretJSON []byte
		if secretJSON, err = service.encryptBindPassword(provider.Alias, input.BindPassword); err != nil {
			return
		}
		provider.EncryptedSecretsJSON = string(secretJSON)
	}
	var configurationJSON []byte
	if configurationJSON, err = json.Marshal(input.LDAPRealmConfiguration); err != nil {
		return
	}
	provider.ConfigurationJSON = string(configurationJSON)
	provider.Enabled = input.Enabled
	if err = service.store.AuthenticationProviders.Update(provider); err != nil {
		return
	}
	realm = AuthenticationRealm{Alias: provider.Alias, Kind: "ldap", Enabled: provider.Enabled, HasBindPassword: provider.EncryptedSecretsJSON != "", Configuration: input.LDAPRealmConfiguration}
	return
}

// SetLocalRealmEnabled toggles local logins while preserving at least one enabled admin identity.
func (service *Service) SetLocalRealmEnabled(enabled bool) (realm AuthenticationRealm, err error) {
	var provider *db.AuthenticationProvider
	if provider, err = service.findProvider("organesson"); err != nil || provider == nil || provider.Kind != db.AuthenticationProviderKindLocal || !provider.SystemManaged {
		if err == nil {
			err = errors.New("built-in local realm is unavailable")
		}
		return
	}
	if !enabled {
		var safeAlternateAdmin bool
		if safeAlternateAdmin, err = service.hasEnabledAdministratorIdentityExcept(provider.ID); err != nil {
			return
		}
		if !safeAlternateAdmin {
			err = errors.New("cannot disable the local realm until a platform administrator can sign in through an enabled LDAP realm")
			return
		}
	}
	provider.Enabled = enabled
	if err = service.store.AuthenticationProviders.Update(provider); err != nil {
		return
	}
	realm = AuthenticationRealm{Alias: provider.Alias, Kind: "local", Enabled: enabled, SystemManaged: true}
	return
}

// TestLDAPRealm verifies secure connection, service bind, and base-DN search access.
func (service *Service) TestLDAPRealm(alias string) (err error) {
	var provider *db.AuthenticationProvider
	if provider, err = service.findProvider(alias); err != nil || provider == nil || provider.Kind != db.AuthenticationProviderKindLDAP {
		if err == nil {
			err = ErrInvalidInput
		}
		return
	}
	var configuration LDAPRealmConfiguration
	if err = json.Unmarshal([]byte(provider.ConfigurationJSON), &configuration); err != nil {
		return
	}
	var password string
	if password, err = service.decryptBindPassword(provider); err != nil {
		return
	}
	var connection *ldap.Conn
	if connection, err = dialLDAP(configuration); err != nil {
		return
	}
	defer connection.Close()
	connection.SetTimeout(10 * time.Second)
	if err = connection.Bind(configuration.BindDN, password); err != nil {
		return errors.New("LDAP service bind failed")
	}
	_, err = connection.Search(ldap.NewSearchRequest(configuration.BaseDN, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, 10, false, "(objectClass=*)", []string{"dn"}, nil))
	return
}

// AuthenticateLDAP locates and verifies a directory identity, then maps it to a local Organesson user.
func (service *Service) authenticateLDAP(provider *db.AuthenticationProvider, username string, password string) (account *db.Account, err error) {
	if !provider.Enabled {
		err = ErrInvalidCredentials
		return
	}
	var configuration LDAPRealmConfiguration
	if err = json.Unmarshal([]byte(provider.ConfigurationJSON), &configuration); err != nil {
		return
	}
	var bindPassword string
	if bindPassword, err = service.decryptBindPassword(provider); err != nil {
		_ = verifyPassword(service.dummyHash, password)
		err = ErrInvalidCredentials
		return
	}
	var connection *ldap.Conn
	if connection, err = dialLDAP(configuration); err != nil {
		_ = verifyPassword(service.dummyHash, password)
		err = ErrInvalidCredentials
		return
	}
	defer connection.Close()
	connection.SetTimeout(10 * time.Second)
	if err = connection.Bind(configuration.BindDN, bindPassword); err != nil {
		_ = verifyPassword(service.dummyHash, password)
		err = ErrInvalidCredentials
		return
	}
	var filter string = strings.ReplaceAll(configuration.UserFilter, "{username}", ldap.EscapeFilter(strings.TrimSpace(username)))
	var results *ldap.SearchResult
	if results, err = connection.Search(ldap.NewSearchRequest(configuration.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 10, false, filter,
		[]string{configuration.UsernameAttribute, configuration.DisplayNameAttribute, configuration.EmailAttribute, "entryUUID", "objectGUID"}, nil)); err != nil || results == nil || len(results.Entries) != 1 {
		_ = verifyPassword(service.dummyHash, password)
		err = ErrInvalidCredentials
		return
	}
	var entry *ldap.Entry = results.Entries[0]
	if err = connection.Bind(entry.DN, password); err != nil {
		_ = verifyPassword(service.dummyHash, password)
		err = ErrInvalidCredentials
		return
	}
	var directoryUser ldapUser
	directoryUser.Username = entry.GetAttributeValue(configuration.UsernameAttribute)
	directoryUser.Name = entry.GetAttributeValue(configuration.DisplayNameAttribute)
	directoryUser.Subject = entry.GetAttributeValue("entryUUID")
	if directoryUser.Subject == "" {
		directoryUser.Subject = entry.GetAttributeValue("objectGUID")
	}
	if directoryUser.Subject == "" {
		directoryUser.Subject = strings.ToLower(entry.DN)
	}
	if directoryUser.Username == "" {
		err = ErrInvalidCredentials
		return
	}
	account, err = service.upsertLDAPIdentity(provider, directoryUser)
	return
}

func (service *Service) upsertLDAPIdentity(provider *db.AuthenticationProvider, user ldapUser) (account *db.Account, err error) {
	var identities []*db.AccountIdentity
	if identities, err = service.store.AccountIdentities.SelectAll(); err != nil {
		return
	}
	var qualifiedName string = strings.ToLower(user.Username) + "@" + provider.Alias
	var subjectKey string = provider.Alias + ":" + user.Subject
	var mappedIdentity *db.AccountIdentity
	for _, identity := range identities {
		if identity.AuthenticationProviderID == provider.ID && identity.ProviderSubjectKey == subjectKey {
			mappedIdentity = identity
			continue
		}
		if strings.EqualFold(identity.QualifiedName, qualifiedName) {
			err = ErrConflict
			return
		}
	}
	var now time.Time = service.now()
	var displayName string = strings.TrimSpace(user.Name)
	if displayName == "" {
		displayName = user.Username
	}
	if mappedIdentity != nil {
		if account, err = service.store.Accounts.Select(mappedIdentity.AccountID); err != nil {
			return
		}
		if account == nil || account.Disabled {
			account = nil
			err = ErrInvalidCredentials
			return
		}
		mappedIdentity.QualifiedName = qualifiedName
		mappedIdentity.ProviderSubject = user.Subject
		if err = service.store.AccountIdentities.Update(mappedIdentity); err != nil {
			return
		}
		account.DisplayName = displayName
		if err = service.store.Accounts.Update(account); err != nil {
			return
		}
		return
	}
	account = &db.Account{DisplayName: displayName, ActivatedAt: &now, CreatedAt: now}
	if err = service.store.Accounts.Insert(account); err != nil {
		return
	}
	var identity *db.AccountIdentity = &db.AccountIdentity{
		AccountID: account.ID, AuthenticationProviderID: provider.ID, ProviderSubject: user.Subject,
		ProviderSubjectKey: provider.Alias + ":" + user.Subject, QualifiedName: qualifiedName, CreatedAt: now,
	}
	if err = service.store.AccountIdentities.Insert(identity); err != nil {
		_ = service.store.Accounts.Delete(account.ID)
		return
	}
	return
}

func (service *Service) findProvider(alias string) (provider *db.AuthenticationProvider, err error) {
	var providers []*db.AuthenticationProvider
	if providers, err = service.store.AuthenticationProviders.SelectAll(); err != nil {
		return
	}
	for _, candidate := range providers {
		if strings.EqualFold(candidate.Alias, strings.TrimSpace(alias)) {
			provider = candidate
			return
		}
	}
	return
}

func (service *Service) encryptBindPassword(alias string, password string) (encoded []byte, err error) {
	var block cipher.Block
	if block, err = aes.NewCipher(service.encryptionKey); err != nil {
		return
	}
	var gcm cipher.AEAD
	if gcm, err = cipher.NewGCM(block); err != nil {
		return
	}
	var nonce []byte = make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return
	}
	var secret ldapBindSecret = ldapBindSecret{
		Version: 1, Nonce: base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(gcm.Seal(nil, nonce, []byte(password), []byte(alias))),
	}
	encoded, err = json.Marshal(secret)
	return
}

func (service *Service) decryptBindPassword(provider *db.AuthenticationProvider) (password string, err error) {
	if len(service.encryptionKey) != 32 {
		err = ErrEncryptionKeyRequired
		return
	}
	var secret ldapBindSecret
	if err = json.Unmarshal([]byte(provider.EncryptedSecretsJSON), &secret); err != nil || secret.Version != 1 {
		err = errors.New("stored LDAP bind credential cannot be read")
		return
	}
	var nonce []byte
	if nonce, err = base64.StdEncoding.DecodeString(secret.Nonce); err != nil {
		return
	}
	var ciphertext []byte
	if ciphertext, err = base64.StdEncoding.DecodeString(secret.Ciphertext); err != nil {
		return
	}
	var block cipher.Block
	if block, err = aes.NewCipher(service.encryptionKey); err != nil {
		return
	}
	var gcm cipher.AEAD
	if gcm, err = cipher.NewGCM(block); err != nil {
		return
	}
	var plaintext []byte
	if plaintext, err = gcm.Open(nil, nonce, ciphertext, []byte(provider.Alias)); err != nil {
		err = errors.New("stored LDAP bind credential cannot be decrypted; verify ORGANESSON_AUTH_ENCRYPTION_KEY")
		return
	}
	password = string(plaintext)
	return
}

func validateLDAPRealm(configuration LDAPRealmConfiguration) (err error) {
	var endpoint *url.URL
	if endpoint, err = url.Parse(configuration.URL); err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		err = errors.New("LDAP URL must be an ldap:// or ldaps:// endpoint without credentials or query parameters")
		return
	}
	if endpoint.Scheme != "ldaps" && endpoint.Scheme != "ldap" {
		err = errors.New("LDAP must use LDAPS or LDAP with StartTLS")
		return
	}
	if configuration.BaseDN == "" || configuration.BindDN == "" || configuration.UserFilter == "" || !strings.Contains(configuration.UserFilter, "{username}") {
		err = errors.New("LDAP base DN, bind DN, and a user filter containing {username} are required")
		return
	}
	for _, attribute := range []string{configuration.UsernameAttribute, configuration.DisplayNameAttribute} {
		if !ldapAttributeName.MatchString(attribute) {
			err = errors.New("LDAP username and display-name attributes must be valid attribute names")
			return
		}
	}
	if configuration.EmailAttribute != "" && !ldapAttributeName.MatchString(configuration.EmailAttribute) {
		err = errors.New("LDAP email attribute must be a valid attribute name")
		return
	}
	var filter string = strings.ReplaceAll(configuration.UserFilter, "{username}", ldap.EscapeFilter("organesson-filter-check"))
	if _, err = ldap.CompileFilter(filter); err != nil {
		err = errors.New("LDAP user filter is invalid")
		return
	}
	if configuration.CACertificatePEM != "" {
		var roots *x509.CertPool = x509.NewCertPool()
		if !roots.AppendCertsFromPEM([]byte(configuration.CACertificatePEM)) {
			err = errors.New("LDAP CA certificate must contain a valid PEM certificate")
		}
	}
	return
}

func dialLDAP(configuration LDAPRealmConfiguration) (connection *ldap.Conn, err error) {
	var tlsConfiguration *tls.Config = &tls.Config{MinVersion: tls.VersionTLS12}
	if configuration.CACertificatePEM != "" {
		tlsConfiguration.RootCAs = x509.NewCertPool()
		if !tlsConfiguration.RootCAs.AppendCertsFromPEM([]byte(configuration.CACertificatePEM)) {
			err = errors.New("LDAP CA certificate is invalid")
			return
		}
	}
	var endpoint *url.URL
	if endpoint, err = url.Parse(configuration.URL); err != nil {
		return
	}
	var dialer *net.Dialer = &net.Dialer{Timeout: 10 * time.Second}
	if connection, err = ldap.DialURL(configuration.URL, ldap.DialWithDialer(dialer), ldap.DialWithTLSConfig(tlsConfiguration)); err != nil {
		return
	}
	connection.SetTimeout(10 * time.Second)
	if endpoint.Scheme == "ldap" {
		if err = connection.StartTLS(tlsConfiguration); err != nil {
			connection.Close()
			connection = nil
			return
		}
	}
	return
}

func (service *Service) hasEnabledAdministratorIdentityExcept(providerID int) (found bool, err error) {
	var providers []*db.AuthenticationProvider
	if providers, err = service.store.AuthenticationProviders.SelectAll(); err != nil {
		return
	}
	var enabledProviderIDs map[int]bool = make(map[int]bool)
	for _, provider := range providers {
		if provider.Enabled && provider.ID != providerID {
			enabledProviderIDs[provider.ID] = true
		}
	}
	var accounts []*db.Account
	if accounts, err = service.store.Accounts.SelectAll(); err != nil {
		return
	}
	var identities []*db.AccountIdentity
	if identities, err = service.store.AccountIdentities.SelectAll(); err != nil {
		return
	}
	for _, account := range accounts {
		if !account.PlatformAdministrator || account.Disabled || account.ActivatedAt == nil {
			continue
		}
		for _, identity := range identities {
			if identity.AccountID == account.ID && enabledProviderIDs[identity.AuthenticationProviderID] {
				found = true
				return
			}
		}
	}
	return
}
