package app

import (
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/csrf"
	"github.com/gofiber/fiber/v3/middleware/helmet"
	"github.com/gofiber/fiber/v3/middleware/limiter"
	"github.com/gofiber/fiber/v3/middleware/session"
	"github.com/z46-dev/organesson/backend/app/api"
)

// New builds the HTTP application with secure browser sessions and CSRF protection.
func New(services api.Services, secureCookies bool, allowedOrigins []string) (application *fiber.App) {
	application = fiber.New(fiber.Config{BodyLimit: 1 << 20})
	application.Use(helmet.New())

	if len(allowedOrigins) > 0 {
		application.Use(cors.New(cors.Config{
			AllowOrigins:     allowedOrigins,
			AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "X-Csrf-Token"},
			AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
			AllowCredentials: true,
		}))
	}

	var sessionMiddleware fiber.Handler
	var sessionStore *session.Store
	sessionMiddleware, sessionStore = session.NewWithStore(session.Config{
		CookiePath:      "/",
		CookieSameSite:  "Lax",
		CookieSecure:    secureCookies,
		CookieHTTPOnly:  true,
		IdleTimeout:     8 * time.Hour,
		AbsoluteTimeout: 24 * time.Hour,
	})
	application.Use(sessionMiddleware)
	application.Use(csrf.New(csrf.Config{
		Session:        sessionStore,
		CookieSecure:   secureCookies,
		CookieHTTPOnly: true,
		CookieSameSite: "Lax",
		Next: func(ctx fiber.Ctx) (skip bool) {
			skip = strings.HasPrefix(ctx.Get(fiber.HeaderAuthorization), "Bearer ")
			return
		},
	}))
	ApplyAuthenticationLimit(application)
	api.Init(application, services)
	return
}

// Start creates the Fiber application and listens on the configured address.
func Start(services api.Services, address string, tlsDirectory string, allowedOrigins []string) (err error) {
	var secureCookies bool = tlsDirectory != ""
	var application *fiber.App = New(services, secureCookies, allowedOrigins)
	var listenConfig fiber.ListenConfig
	if secureCookies {
		var certPath, keyPath string
		var found bool
		if certPath, keyPath, found = discoverTLSKeys(tlsDirectory); !found {
			err = errors.New("TLS certificate and private key were not found in the configured directory")
			return
		}
		listenConfig.CertFile = certPath
		listenConfig.CertKeyFile = keyPath
	}
	err = application.Listen(address, listenConfig)
	return
}

// ApplyAuthenticationLimit limits password and bootstrap attempts per client address.
func ApplyAuthenticationLimit(application *fiber.App) {
	var authLimiter fiber.Handler = limiter.New(limiter.Config{
		Max:        10,
		Expiration: time.Minute,
	})
	application.Use("/api/v1/auth/login", authLimiter)
	application.Use("/api/v1/auth/bootstrap/redeem", limiter.New(limiter.Config{
		Max:        5,
		Expiration: time.Minute,
	}))
}
