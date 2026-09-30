package organesson

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var errRemoteNotFound = errors.New("Organesson resource was not found")

type apiClient struct {
	endpoint string
	token    string
	client   *http.Client
}

// request sends one authenticated request to the Organesson API.
func (client *apiClient) request(ctx context.Context, method string, path string, requestBody any, result any) (err error) {
	var body io.Reader
	if requestBody != nil {
		var encoded []byte
		if encoded, err = json.Marshal(requestBody); err != nil {
			return
		}
		body = bytes.NewReader(encoded)
	}
	var request *http.Request
	if request, err = http.NewRequestWithContext(ctx, method, client.endpoint+path, body); err != nil {
		return
	}
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("Accept", "application/json")
	if requestBody != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	var response *http.Response
	if response, err = client.client.Do(request); err != nil {
		return
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		err = errRemoteNotFound
		return
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var responseBody []byte
		responseBody, _ = io.ReadAll(io.LimitReader(response.Body, 4096))
		err = fmt.Errorf("Organesson API %s %s returned %s: %s", method, path, response.Status, strings.TrimSpace(string(responseBody)))
		return
	}
	if result != nil && response.StatusCode != http.StatusNoContent {
		err = json.NewDecoder(response.Body).Decode(result)
	}
	return
}

// configuredClient validates endpoint and creates the bounded HTTP client used by provider resources.
func configuredClient(endpoint string, token string) (client *apiClient, err error) {
	var parsed *url.URL
	if parsed, err = url.ParseRequestURI(strings.TrimSpace(endpoint)); err != nil {
		return
	}
	if (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		err = errors.New("endpoint must be an HTTP(S) origin without credentials, query, or fragment")
		return
	}
	if strings.TrimSpace(token) == "" {
		err = errors.New("provider token is required")
		return
	}
	client = &apiClient{
		endpoint: strings.TrimRight(parsed.String(), "/"),
		token:    token,
		client:   &http.Client{Timeout: 20 * time.Second},
	}
	return
}
