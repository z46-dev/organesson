package v1

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v3"
	"github.com/valyala/fasthttp"
	"github.com/z46-dev/organesson/backend/app/api/common"
	"github.com/z46-dev/organesson/backend/db"
	"github.com/z46-dev/organesson/backend/domain"
)

// vmConsole upgrades an authorized browser request and proxies it to the managed VM's PVE console.
func vmConsole(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var resourceID int
		if resourceID, err = common.ParseID(ctx, "resource_id"); err != nil {
			return ctx.SendStatus(fiber.StatusBadRequest)
		}
		var resource *db.ManagedResource
		if resource, err = ctxResource(services, actorID, resourceID); err != nil {
			return common.DomainError(ctx, err)
		}
		if services.Proxmox == nil {
			return ctx.SendStatus(fiber.StatusServiceUnavailable)
		}
		var upgrader websocket.FastHTTPUpgrader = websocket.FastHTTPUpgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin: func(request *fasthttp.RequestCtx) bool {
				var origin string = string(request.Request.Header.Peek("Origin"))
				var parsed *url.URL
				var parseErr error
				if parsed, parseErr = url.Parse(origin); parseErr != nil || parsed.Host == "" {
					return false
				}
				return strings.EqualFold(parsed.Host, string(request.Host()))
			},
		}
		err = upgrader.Upgrade(ctx.RequestCtx(), func(browser *websocket.Conn) {
			defer browser.Close()
			var upstream *websocket.Conn
			var password string
			var dialErr error
			if upstream, password, dialErr = services.Proxmox.DialVMConsole(context.Background(), resource.ExternalNode, resource.ExternalID, resource.OperationKey); dialErr != nil {
				_ = browser.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "Proxmox console unavailable"), time.Now().Add(time.Second))
				return
			}
			defer upstream.Close()
			if browser.WriteMessage(websocket.TextMessage, []byte(password)) != nil {
				return
			}
			browser.SetReadLimit(32 << 20)
			upstream.SetReadLimit(32 << 20)
			proxyConsole(browser, upstream)
		})
		return
	}
	return
}

// ctxResource resolves a VM only after checking its dedicated console permission.
func ctxResource(services common.Services, actorID int, resourceID int) (resource *db.ManagedResource, err error) {
	if resource, err = services.Store.ManagedResources.Select(resourceID); err != nil {
		return
	}
	if resource == nil || resource.Kind != "virtual_machine" || resource.ExternalID == "" {
		err = domain.ErrNotFound
		return
	}
	err = services.Domain.Require(actorID, db.PermissionVMConsole, resource.OwnershipID)
	return
}

// proxyConsole forwards VNC WebSocket frames in both directions without exposing PVE credentials.
func proxyConsole(browser *websocket.Conn, upstream *websocket.Conn) {
	var finished chan struct{} = make(chan struct{}, 2)
	var copyFrames = func(destination *websocket.Conn, source *websocket.Conn) {
		for {
			var messageType int
			var payload []byte
			if messageType, payload, _ = source.ReadMessage(); messageType == 0 {
				break
			}
			if destination.WriteMessage(messageType, payload) != nil {
				break
			}
		}
		finished <- struct{}{}
	}
	go copyFrames(upstream, browser)
	go copyFrames(browser, upstream)
	<-finished
	_ = browser.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
}
