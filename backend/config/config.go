package config

import (
	"errors"
	"strings"

	"github.com/z46-dev/goconf"
)

type Configuration struct {
	WebServer struct {
		Address            string   `toml:"address" default:":6800" validate:"required"` // Listen address for the web application server e.g. ":6800" or "0.0.0.0:6800"
		TLSDir             string   `toml:"tls_dir" default:""`                          // Directory containing a certificate and key file for TLS.
		CORSAllowedOrigins []string `toml:"cors_allowed_origins" default:"[]"`           // Explicit browser origins allowed to access the API.
	} `toml:"web_server"` // Web server configuration

	Database struct {
		File string `toml:"file" default:"organesson.db" validate:"required"` // Path to the SQLite database file
	} `toml:"database"` // Database configuration

	Development struct {
		EnableTestFixtures bool `toml:"enable_test_fixtures" default:"false"` // Explicitly permit local test fixture commands.
	} `toml:"development"` // Opt-in development-only features.
}

var Cfg Configuration

// Init will try to load the configuration from the specified path and initialize the global Cfg variable.
func Init(path string) (err error) {
	Cfg, err = goconf.LoadConfig[Configuration](path, goconf.WithIndentSpaces(4), goconf.WithNewFileBehavior(goconf.NewFileBehaviorCreateAndTry))
	if err != nil {
		return
	}
	for _, origin := range Cfg.WebServer.CORSAllowedOrigins {
		if strings.TrimSpace(origin) == "*" {
			err = errors.New("web_server.cors_allowed_origins must list explicit origins; wildcard origins are not allowed")
			return
		}
	}
	return
}
