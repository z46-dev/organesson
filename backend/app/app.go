package app

import (
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/csrf"
	"github.com/z46-dev/organesson/backend/app/api"
	"github.com/z46-dev/organesson/backend/config"
)

var app *fiber.App

func Start() (err error) {
	app = fiber.New(fiber.Config{})

	app.Use(cors.New(cors.Config{
		AllowOrigins: config.Cfg.WebServer.CORSAllowedOrigins,
		AllowHeaders: []string{"Origin", "Content-Type", "Accept", "Authorization"},
	}))

	if config.Cfg.WebServer.EnableCSRF {
		app.Use(csrf.New(csrf.Config{
			Next: func(ctx fiber.Ctx) bool {
				return strings.HasPrefix(ctx.Path(), "/api")
			},
		}))
	}

	api.Init(app)

	var listenConfig fiber.ListenConfig
	if config.Cfg.WebServer.TLSDir != "" {
		var (
			certPath, keyPath string
			found             bool
		)

		if certPath, keyPath, found = discoverTLSKeys(config.Cfg.WebServer.TLSDir); !found {
			err = fiber.ErrInternalServerError
			return
		}

		listenConfig.CertFile = certPath
		listenConfig.CertKeyFile = keyPath
	}

	err = app.Listen(config.Cfg.WebServer.Address, listenConfig)
	return
}
