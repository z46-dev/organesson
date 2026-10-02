package v1

import (
	"github.com/gofiber/fiber/v3"
	"github.com/z46-dev/organesson/backend/app/api/common"
	"github.com/z46-dev/organesson/backend/app/api/v1/auth"
)

// Init registers the versioned API.
func Init(parent fiber.Router, services common.Services) {
	var v1Router fiber.Router = parent.Group("/v1")
	auth.Init(v1Router, services)
	initAccounts(v1Router, services)
	initDeployments(v1Router, services)
	initVMTemplates(v1Router, services)
	initProxmoxResources(v1Router, services)
}
