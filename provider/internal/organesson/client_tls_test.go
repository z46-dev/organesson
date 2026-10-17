package organesson

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestConfiguredClientCanSkipCertificateVerification verifies the explicit lab-only TLS override.
func TestConfiguredClientCanSkipCertificateVerification(t *testing.T) {
	var server *httptest.Server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	var client *apiClient
	var err error
	if client, err = configuredClient(server.URL, "test-token", "", false); err != nil {
		t.Fatalf("configure client with default TLS verification: %v", err)
	}
	if err = client.request(context.Background(), http.MethodGet, "/health", nil, nil); err == nil {
		t.Fatal("default TLS verification accepted an untrusted certificate")
	}

	if client, err = configuredClient(server.URL, "test-token", "", true); err != nil {
		t.Fatalf("configure client with TLS verification disabled: %v", err)
	}
	if err = client.request(context.Background(), http.MethodGet, "/health", nil, nil); err != nil {
		t.Fatalf("request to untrusted test certificate: %v", err)
	}
}
