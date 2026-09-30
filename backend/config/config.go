package config

import (
	"github.com/z46-dev/goconf"
)

type Configuration struct {
	WebServer struct {
		Address            string   `toml:"address" default:":6800" validate:"required"` // Listen address for the web application server e.g. ":6800" or "0.0.0.0:6800"
		TLSDir             string   `toml:"tls_dir" default:""`                          // Directory containing a crt and a key file for TLS. Leave empty to use HTTP instead of HTTPS.
		EnableCSRF         bool     `toml:"enable_csrf" default:"true"`                  // Enable CSRF protection middleware
		CORSAllowedOrigins []string `toml:"cors_allowed_origins" default:"[\"*\"]"`      // CORS allowed origins for the web server
	} `toml:"web_server"` // Web server configuration

	Database struct {
		File string `toml:"file" default:"organesson.db" validate:"required"` // Path to the MySQL database file
	} `toml:"database"` // Database configuration
}

var Cfg Configuration

// Init will try to load the configuration from the specified path and initialize the global Cfg variable.
func Init(path string) (err error) {
	Cfg, err = goconf.LoadConfig[Configuration](path, goconf.WithIndentSpaces(4), goconf.WithNewFileBehavior(goconf.NewFileBehaviorCreateAndTry))
	return
}
