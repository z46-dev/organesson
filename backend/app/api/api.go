package api

import (
	"github.com/gofiber/fiber/v3"
	v1 "github.com/z46-dev/organesson/backend/app/api/v1"
)

// Simple health-check endpoint to verify that the API is loaded and running. Returns a 200 OK response.
func Ping(ctx fiber.Ctx) (err error) {
	err = ctx.SendStatus(fiber.StatusOK)
	return
}

func Init(app *fiber.App) {
	var apiGroup = app.Group("/api")
	apiGroup.Get("/ping", Ping)

	v1.Init(apiGroup)
}
