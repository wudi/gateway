package auth

import (
	"net/http"
	"sync"

	"golang.org/x/crypto/bcrypt"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/errors"
	"github.com/wudi/runway/variables"
)

type basicUserData struct {
	passwordHash []byte
	clientID     string
	roles        []string
}

// BasicAuth provides HTTP Basic Authentication against a local user list.
type BasicAuth struct {
	realm     string
	users     map[string]*basicUserData
	dummyHash []byte // timing-safe comparison for unknown users
	mu        sync.RWMutex
}

// NewBasicAuth creates a new Basic authenticator.
func NewBasicAuth(cfg config.BasicAuthConfig) *BasicAuth {
	realm := cfg.Realm
	if realm == "" {
		realm = "Restricted"
	}

	users := make(map[string]*basicUserData, len(cfg.Users))
	for _, u := range cfg.Users {
		users[u.Username] = &basicUserData{
			passwordHash: []byte(u.PasswordHash),
			clientID:     u.ClientID,
			roles:        u.Roles,
		}
	}

	// Pre-compute a dummy hash so we can run bcrypt.CompareHashAndPassword even
	// for unknown usernames, preventing timing-based user enumeration.
	dummyHash, _ := bcrypt.GenerateFromPassword([]byte("dummy"), bcrypt.DefaultCost)

	return &BasicAuth{
		realm:     realm,
		users:     users,
		dummyHash: dummyHash,
	}
}

// Authenticate verifies Basic credentials from the request.
func (a *BasicAuth) Authenticate(r *http.Request) (*variables.Identity, error) {
	username, password, ok := r.BasicAuth()
	if !ok {
		return nil, errors.ErrUnauthorized.WithDetails("Basic credentials not provided")
	}

	a.mu.RLock()
	user, found := a.users[username]
	a.mu.RUnlock()

	if !found {
		// Run bcrypt against dummy hash to prevent timing side-channel
		bcrypt.CompareHashAndPassword(a.dummyHash, []byte(password))
		return nil, errors.ErrUnauthorized.WithDetails("Invalid credentials")
	}

	if err := bcrypt.CompareHashAndPassword(user.passwordHash, []byte(password)); err != nil {
		return nil, errors.ErrUnauthorized.WithDetails("Invalid credentials")
	}

	claims := map[string]interface{}{
		"username": username,
	}
	if len(user.roles) > 0 {
		claims["roles"] = user.roles
	}

	return &variables.Identity{
		ClientID: user.clientID,
		AuthType: "basic",
		Claims:   claims,
	}, nil
}

// IsEnabled returns true if at least one user is configured.
func (a *BasicAuth) IsEnabled() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.users) > 0
}

// Realm returns the configured Basic Auth realm.
func (a *BasicAuth) Realm() string {
	return a.realm
}
