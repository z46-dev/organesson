package v1

import (
	"github.com/gofiber/fiber/v3"
	"github.com/z46-dev/organesson/backend/app/api/v1/auth"
)

func Init(parent fiber.Router) {
	var v1Router = parent.Group("/v1")
	auth.Init(v1Router)
}
