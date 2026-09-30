package auth

import (
	"github.com/gofiber/fiber/v3"
)

func Init(parent fiber.Router) {
	var authRouter = parent.Group("/auth")

	authRouter.Get("/status", status)
}

func status(ctx fiber.Ctx) (err error) {
	err = ctx.SendStatus(fiber.StatusOK)
	return
}
