package organesson

import (
	"errors"
	"testing"
)

func TestRetryableGuestSetupError(t *testing.T) {
	var cases = []struct {
		name                  string
		err                   error
		retryNetworkReadiness bool
		want                  bool
	}{
		{name: "gateway unavailable", err: errors.New(`guest setup returned {"exit_code":11}`), retryNetworkReadiness: true, want: true},
		{name: "dns unavailable", err: errors.New(`guest setup returned {"exit_code":12}`), retryNetworkReadiness: true, want: true},
		{name: "readiness retry is scoped", err: errors.New(`guest setup returned {"exit_code":11}`), want: false},
		{name: "other guest failure", err: errors.New(`guest setup returned {"exit_code":1}`), retryNetworkReadiness: true, want: false},
		{name: "transport error", err: errors.New(`guest setup returned {"exit_code":-1}`), want: true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := retryableGuestSetupError(testCase.err, testCase.retryNetworkReadiness); got != testCase.want {
				t.Fatalf("retryableGuestSetupError() = %t, want %t", got, testCase.want)
			}
		})
	}
}
