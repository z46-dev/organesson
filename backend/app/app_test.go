package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/z46-dev/golog"
	"github.com/z46-dev/organesson/backend/app/api"
	localauth "github.com/z46-dev/organesson/backend/auth"
	"github.com/z46-dev/organesson/backend/db"
	"github.com/z46-dev/organesson/backend/domain"
)

// TestHTTPBoundaryReportsSetupAndRejectsUnauthenticatedMutations checks the initial API boundary.
func TestHTTPBoundaryReportsSetupAndRejectsUnauthenticatedMutations(t *testing.T) {
	var store *db.Store
	var err error
	if store, err = db.Open(filepath.Join(t.TempDir(), "organesson.db"), golog.New(), false); err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer store.Close()

	var authentication *localauth.Service
	if authentication, err = localauth.New(store); err != nil {
		t.Fatalf("create authentication service: %v", err)
	}
	var application *fiber.App = New(api.Services{
		Authentication: authentication,
		Domain:         domain.New(store),
		Store:          store,
	}, false, nil)

	var response *http.Response
	if response, err = application.Test(httptest.NewRequest("GET", "/api/v1/auth/status", nil)); err != nil {
		t.Fatalf("request auth status: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("expected auth status 200, got %d", response.StatusCode)
	}

	if response, err = application.Test(httptest.NewRequest("GET", "/api/v1/deployments", nil)); err != nil {
		t.Fatalf("request protected deployment list: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("expected unauthenticated deployment list to return 401, got %d", response.StatusCode)
	}

	if response, err = application.Test(httptest.NewRequest("POST", "/api/v1/deployments", nil)); err != nil {
		t.Fatalf("request mutation without CSRF token: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != fiber.StatusForbidden {
		t.Fatalf("expected mutation without CSRF token to return 403, got %d", response.StatusCode)
	}
}

// TestBootstrapLoginAndAuthorizedOperations exercises the browser session and API lifecycle.
func TestBootstrapLoginAndAuthorizedOperations(t *testing.T) {
	var store *db.Store
	var err error
	if store, err = db.Open(filepath.Join(t.TempDir(), "organesson.db"), golog.New(), false); err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer store.Close()

	var authentication *localauth.Service
	if authentication, err = localauth.New(store); err != nil {
		t.Fatalf("create authentication service: %v", err)
	}
	var bootstrapToken string
	var created bool
	if bootstrapToken, created, err = authentication.EnsureInitialActivationLink(); err != nil || !created {
		t.Fatalf("create bootstrap token: created=%t err=%v", created, err)
	}
	var application *fiber.App = New(api.Services{
		Authentication: authentication,
		Domain:         domain.New(store),
		Store:          store,
	}, false, nil)
	var jar *cookiejar.Jar
	if jar, err = cookiejar.New(nil); err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}

	var csrfToken string
	if csrfToken, err = requestCSRFToken(t, application, jar); err != nil {
		t.Fatalf("get bootstrap CSRF token: %v", err)
	}
	var response *http.Response
	response = performRequest(t, application, jar, http.MethodPost, "/api/v1/auth/bootstrap/redeem", `{"token":"`+bootstrapToken+`","password":"Smoke-test-password-2026!"}`, csrfToken)
	response.Body.Close()
	if response.StatusCode != fiber.StatusCreated {
		t.Fatalf("expected bootstrap to return 201, got %d", response.StatusCode)
	}

	if csrfToken, err = requestCSRFToken(t, application, jar); err != nil {
		t.Fatalf("get deployment CSRF token: %v", err)
	}
	response = performRequest(t, application, jar, http.MethodPost, "/api/v1/auth/api-tokens", `{"name":"provider acceptance","lifetime_days":7}`, csrfToken)
	var apiTokenResult struct {
		ID    int    `json:"id"`
		Token string `json:"token"`
	}
	if response.StatusCode != fiber.StatusCreated {
		var body []byte
		body, _ = io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("expected API token create 201, got %d: %s", response.StatusCode, body)
	}
	if err = json.NewDecoder(response.Body).Decode(&apiTokenResult); err != nil {
		response.Body.Close()
		t.Fatalf("decode API token response: %v", err)
	}
	response.Body.Close()
	if apiTokenResult.ID < 1 || apiTokenResult.Token == "" {
		t.Fatalf("API token response lacks one-time secret or identifier: %#v", apiTokenResult)
	}
	response = performBearerRequest(t, application, http.MethodPost, "/api/v1/deployments", `{"name":"provider-deployment","description":"Bearer API acceptance"}`, apiTokenResult.Token)
	response.Body.Close()
	if response.StatusCode != fiber.StatusCreated {
		t.Fatalf("expected bearer-authenticated mutation without CSRF to return 201, got %d", response.StatusCode)
	}
	response = performBearerRequest(t, application, http.MethodGet, "/api/v1/deployments", "", "invalid-token")
	response.Body.Close()
	if response.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("expected invalid bearer credential to return 401, got %d", response.StatusCode)
	}
	if csrfToken, err = requestCSRFToken(t, application, jar); err != nil {
		t.Fatalf("get token revoke CSRF token: %v", err)
	}
	response = performRequest(t, application, jar, http.MethodDelete, "/api/v1/auth/api-tokens/"+strconv.Itoa(apiTokenResult.ID), "", csrfToken)
	response.Body.Close()
	if response.StatusCode != fiber.StatusNoContent {
		t.Fatalf("expected API token revocation to return 204, got %d", response.StatusCode)
	}
	response = performBearerRequest(t, application, http.MethodGet, "/api/v1/deployments", "", apiTokenResult.Token)
	response.Body.Close()
	if response.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("expected revoked bearer credential to return 401, got %d", response.StatusCode)
	}

	response = performRequest(t, application, jar, http.MethodPost, "/api/v1/deployments", `{"name":"smoke-deployment","description":"API test"}`, csrfToken)
	var deploymentResult struct {
		Deployment struct {
			ID         int `json:"id"`
			RootNodeID int `json:"root_node_id"`
		} `json:"deployment"`
	}
	if response.StatusCode != fiber.StatusCreated {
		var body []byte
		body, _ = io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("expected deployment create 201, got %d: %s", response.StatusCode, body)
	}
	if err = json.NewDecoder(response.Body).Decode(&deploymentResult); err != nil {
		response.Body.Close()
		t.Fatalf("decode deployment response: %v", err)
	}
	response.Body.Close()
	if deploymentResult.Deployment.ID < 1 || deploymentResult.Deployment.RootNodeID < 1 {
		t.Fatalf("deployment response is missing its IDs: %#v", deploymentResult.Deployment)
	}

	var createVMPath string = "/api/v1/deployments/" + strconv.Itoa(deploymentResult.Deployment.ID) + "/virtual-machines"
	var createVMBody string = `{"parent_node_id":` + strconv.Itoa(deploymentResult.Deployment.RootNodeID) + `,"name":"test-vm"}`
	response = performRequest(t, application, jar, http.MethodPost, createVMPath, createVMBody, csrfToken)
	var vmResult struct {
		Resource struct {
			ID         int    `json:"id"`
			PowerState string `json:"power_state"`
		} `json:"resource"`
	}
	if response.StatusCode != fiber.StatusCreated {
		var body []byte
		body, _ = io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("expected VM create 201, got %d: %s", response.StatusCode, body)
	}
	if err = json.NewDecoder(response.Body).Decode(&vmResult); err != nil {
		response.Body.Close()
		t.Fatalf("decode VM response: %v", err)
	}
	response.Body.Close()
	var powerPath string = "/api/v1/virtual-machines/" + strconv.Itoa(vmResult.Resource.ID) + "/power"
	response = performRequest(t, application, jar, http.MethodPost, powerPath, `{"action":"start"}`, csrfToken)
	response.Body.Close()
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("expected authorized VM power action 200, got %d", response.StatusCode)
	}

	response = performRequest(t, application, jar, http.MethodPost, "/api/v1/deployments", `{"name":"blocked","description":"csrf missing"}`, "")
	response.Body.Close()
	if response.StatusCode != fiber.StatusForbidden {
		t.Fatalf("expected state change without CSRF token to return 403, got %d", response.StatusCode)
	}

	if csrfToken, err = requestCSRFToken(t, application, jar); err != nil {
		t.Fatalf("get logout CSRF token: %v", err)
	}
	response = performRequest(t, application, jar, http.MethodPost, "/api/v1/auth/logout", "", csrfToken)
	response.Body.Close()
	if response.StatusCode != fiber.StatusNoContent {
		t.Fatalf("expected logout to return 204, got %d", response.StatusCode)
	}
	response = performRequest(t, application, jar, http.MethodGet, "/api/v1/deployments", "", "")
	response.Body.Close()
	if response.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("expected request after logout to return 401, got %d", response.StatusCode)
	}
}

func requestCSRFToken(t *testing.T, application *fiber.App, jar *cookiejar.Jar) (token string, err error) {
	t.Helper()
	var response *http.Response = performRequest(t, application, jar, http.MethodGet, "/api/v1/auth/csrf", "", "")
	defer response.Body.Close()
	var result struct {
		Token string `json:"csrf_token"`
	}
	if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
		return
	}
	token = result.Token
	if response.StatusCode != fiber.StatusOK || token == "" {
		t.Fatalf("expected a CSRF token response, got status %d", response.StatusCode)
	}
	return
}

func performRequest(t *testing.T, application *fiber.App, jar *cookiejar.Jar, method string, path string, body string, csrfToken string) (response *http.Response) {
	return performRequestWithAuthorization(t, application, jar, method, path, body, csrfToken, "")
}

func performBearerRequest(t *testing.T, application *fiber.App, method string, path string, body string, token string) (response *http.Response) {
	return performRequestWithAuthorization(t, application, nil, method, path, body, "", "Bearer "+token)
}

func performRequestWithAuthorization(t *testing.T, application *fiber.App, jar *cookiejar.Jar, method string, path string, body string, csrfToken string, authorization string) (response *http.Response) {
	t.Helper()
	var requestURL *url.URL
	var err error
	if requestURL, err = url.Parse("http://organesson.test" + path); err != nil {
		t.Fatalf("parse request URL: %v", err)
	}
	var requestBody io.Reader
	if body != "" {
		requestBody = strings.NewReader(body)
	}
	var request *http.Request
	if request, err = http.NewRequest(method, requestURL.String(), requestBody); err != nil {
		t.Fatalf("create request: %v", err)
	}
	if jar != nil {
		for _, cookie := range jar.Cookies(requestURL) {
			request.AddCookie(cookie)
		}
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if csrfToken != "" {
		request.Header.Set("X-Csrf-Token", csrfToken)
	}
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	if response, err = application.Test(request); err != nil {
		t.Fatalf("perform %s %s: %v", method, path, err)
	}
	if jar != nil {
		jar.SetCookies(requestURL, response.Cookies())
	}
	return
}
