package api

import (
	"github.com/gofiber/fiber/v3"
	"github.com/z46-dev/organesson/backend/app/api/common"
	v1 "github.com/z46-dev/organesson/backend/app/api/v1"
)

type Services = common.Services

// Ping verifies that the API server is responding.
func Ping(ctx fiber.Ctx) (err error) {
	err = ctx.SendStatus(fiber.StatusOK)
	return
}

// Init registers health, authentication, and domain routes.
func Init(app *fiber.App, services Services) {
	var apiGroup fiber.Router = app.Group("/api")
	apiGroup.Get("/ping", Ping)
	v1.Init(apiGroup, services)
}
