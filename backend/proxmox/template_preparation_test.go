package proxmox

import (
	"context"
	"testing"
)

// TestTemplatePreparationValidation keeps guest-agent and OS-family gates explicit.
func TestTemplatePreparationValidation(t *testing.T) {
	var cases = []struct {
		expected string
		detected string
		matches  bool
	}{
		{expected: "fedora", detected: "fedora", matches: true},
		{expected: "rhel", detected: "rocky", matches: true},
		{expected: "opensuse", detected: "opensuse-leap", matches: true},
		{expected: "windows", detected: "windows", matches: true},
		{expected: "bsd", detected: "freebsd", matches: true},
		{expected: "debian", detected: "ubuntu", matches: false},
		{expected: "bsd", detected: "openbsd", matches: false},
	}
	for _, testCase := range cases {
		if actual := guestOSMatchesTemplate(testCase.expected, testCase.detected); actual != testCase.matches {
			t.Errorf("guestOSMatchesTemplate(%q, %q) = %t, want %t", testCase.expected, testCase.detected, actual, testCase.matches)
		}
	}

	if guestAgentEnabled("1,freeze-fs-on-backup=0") != true || guestAgentEnabled("0") {
		t.Fatal("QEMU Guest Agent setting parsing did not distinguish enabled and disabled")
	}
	for _, username := range []string{"alice", "administrator", "svc-user$"} {
		if !validLinuxDeletionName(username) {
			t.Errorf("valid account name %q was rejected", username)
		}
	}
	for _, username := range []string{"", "root", "-rf", "bad;name", "name with space", "very-long-account-name-that-is-not-supported"} {
		if validLinuxDeletionName(username) {
			t.Errorf("unsafe account name %q unexpectedly passed validation", username)
		}
	}
	for _, username := range []string{"alice", "Administrator", "guest.name"} {
		if !validWindowsDeletionName(username) {
			t.Errorf("valid Windows account name %q was rejected", username)
		}
	}
	for _, username := range []string{"", "-admin", "domain\\user", "user name", "bad;name"} {
		if validWindowsDeletionName(username) {
			t.Errorf("unsafe Windows account name %q unexpectedly passed validation", username)
		}
	}
	if err := ValidateTemplatePreparationScripts("linux", map[string]string{"linux": "#!/bin/bash"}); err != nil {
		t.Fatalf("valid Linux prep bundle rejected: %v", err)
	}
	if err := ValidateTemplatePreparationScripts("windows", map[string]string{"windows-common": "common", "windows-entry": "entry"}); err != nil {
		t.Fatalf("valid Windows prep bundle rejected: %v", err)
	}
	if err := ValidateTemplatePreparationScripts("bsd", map[string]string{"freebsd": "#!/bin/sh"}); err != nil {
		t.Fatalf("valid FreeBSD prep bundle rejected: %v", err)
	}
	if err := ValidateTemplatePreparationScripts("windows", map[string]string{"windows-common": "common", "arbitrary": "code"}); err == nil {
		t.Fatal("mismatched Windows prep bundle was accepted")
	}
}

type fakeTemplatePreparationDriver struct {
	request TemplatePreparationRequest
}

type fakeTemplateDetectionDriver struct {
	sourceID  string
	guestType string
}

func (driver *fakeTemplatePreparationDriver) Prepare(_ context.Context, request TemplatePreparationRequest) (result PreflightResult, err error) {
	driver.request = request
	result = PreflightResult{Passed: true}
	return
}

func (driver *fakeTemplateDetectionDriver) Detect(_ context.Context, sourceID string, guestType string) (result PreflightResult, err error) {
	driver.sourceID = sourceID
	driver.guestType = guestType
	result = PreflightResult{SourceID: sourceID, GuestOSName: "Fedora Linux 44", GuestOSVersion: "44", GuestArchitecture: "x86_64", Passed: true, PowerState: "stopped"}
	return
}

// TestDetectTemplateValidatesTheBroadGuestTypeAndReturnsDetectedMetadata keeps registration behind server-side detection.
func TestDetectTemplateValidatesTheBroadGuestTypeAndReturnsDetectedMetadata(t *testing.T) {
	var detector *fakeTemplateDetectionDriver = &fakeTemplateDetectionDriver{}
	var service *Service = NewWithTemplateCatalogDrivers(nil, detector)
	var result PreflightResult
	var err error
	if result, err = service.DetectTemplate(context.Background(), "156", "linux"); err != nil {
		t.Fatalf("detect Linux source: %v", err)
	}
	if detector.sourceID != "156" || detector.guestType != "linux" || result.GuestOSName != "Fedora Linux 44" || result.GuestArchitecture != "x86_64" {
		t.Fatalf("source detection did not preserve requested and detected metadata: result=%#v detector=%#v", result, detector)
	}
	if _, err = service.DetectTemplate(context.Background(), "156", "fedora"); err == nil {
		t.Fatal("a specific distribution was accepted where only broad guest types are allowed")
	}
}

// TestPrepareTemplateValidatesAndPassesTheScript exercises the service boundary without contacting Proxmox.
func TestPrepareTemplateValidatesAndPassesTheScript(t *testing.T) {
	var driver *fakeTemplatePreparationDriver = &fakeTemplatePreparationDriver{}
	var service *Service = NewWithTemplatePreparationDriver(driver)
	var request TemplatePreparationRequest = TemplatePreparationRequest{SourceID: "157", ExpectedOS: "fedora", Scripts: map[string]string{"linux": "#!/bin/bash\nexit 0"}}
	if result, err := service.PrepareTemplate(context.Background(), request); err != nil || !result.Passed {
		t.Fatalf("prepare template returned result=%#v err=%v", result, err)
	}
	if driver.request.Scripts["linux"] != request.Scripts["linux"] || driver.request.SourceID != request.SourceID {
		t.Fatalf("preparation request was not passed through: %#v", driver.request)
	}
	if _, err := service.PrepareTemplate(context.Background(), TemplatePreparationRequest{SourceID: "157"}); err == nil {
		t.Fatal("empty preparation script was accepted")
	}
}
