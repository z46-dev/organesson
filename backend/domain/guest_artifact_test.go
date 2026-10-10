package domain

import "testing"

// TestArtifactGuestOSFamily keeps guest execution restricted to supported prepared OS families.
func TestArtifactGuestOSFamily(t *testing.T) {
	var cases = []struct {
		guestOS string
		family  string
	}{
		{guestOS: "fedora", family: "linux"},
		{guestOS: "windows", family: "windows"},
		{guestOS: "Windows Server 2025", family: "windows"},
		{guestOS: "freebsd", family: "bsd"},
		{guestOS: "bsd", family: "bsd"},
		{guestOS: "pfSense CE", family: "bsd"},
	}
	for _, testCase := range cases {
		if family := artifactGuestOSFamily(testCase.guestOS); family != testCase.family {
			t.Errorf("artifactGuestOSFamily(%q) = %q, want %q", testCase.guestOS, family, testCase.family)
		}
	}
}
