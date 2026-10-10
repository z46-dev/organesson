package proxmox

import (
	"context"
	"encoding/xml"
	"io"
	"strings"
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
	if !windowsFirstBootStateValidated("IMAGE_STATE_COMPLETE") {
		t.Fatal("Windows that completed OOBE automatically was rejected")
	}
	for _, testCase := range []struct {
		status string
		want   bool
	}{
		{status: "IMAGE_STATE_COMPLETE|1|1|0", want: true},
		{status: "IMAGE_STATE_COMPLETE|1|1|1", want: false},
		{status: "IMAGE_STATE_COMPLETE|0|1|0", want: false},
		{status: "IMAGE_STATE_COMPLETE|1|0|0", want: false},
		{status: "IMAGE_STATE_UNDEPLOYABLE|1|1|0", want: false},
		{status: "malformed", want: false},
	} {
		if actual, _ := windowsFirstBootReadinessValidated(testCase.status); actual != testCase.want {
			t.Errorf("windowsFirstBootReadinessValidated(%q) = %t, want %t", testCase.status, actual, testCase.want)
		}
	}
	for _, imageState := range []string{"IMAGE_STATE_GENERALIZE_RESEAL_TO_OOBE", "IMAGE_STATE_SPECIALIZE_RESEAL_TO_OOBE"} {
		if !windowsSourceAlreadySealed(imageState) {
			t.Errorf("already sealed Windows state %q was not recognized", imageState)
		}
	}
	if windowsSourceAlreadySealed("IMAGE_STATE_COMPLETE") || windowsSourceAlreadySealed("IMAGE_STATE_UNDEPLOYABLE") {
		t.Fatal("Windows image not waiting for OOBE was incorrectly considered sealed")
	}
	var temporaryLoginCommand string = windowsTemporaryLoginSetupCommand("Og-test-password!aA7")
	for _, setting := range []string{"ogprep", "AutoAdminLogon", "AutoLogonCount", "DefaultPassword", "DefaultDomainName"} {
		if !strings.Contains(temporaryLoginCommand, setting) {
			t.Errorf("temporary Windows preparation login command is missing %s", setting)
		}
	}
	if !strings.Contains(windowsTemporaryLoginCleanupCommand, "DefaultPassword") || !strings.Contains(windowsTemporaryLoginCleanupCommand, "Disable-LocalUser") {
		t.Fatal("temporary Windows autologon cleanup must remove the plaintext password and disable its account")
	}
	if !strings.Contains(windowsTemporaryLoginStateCommand, "ORGANESSON_PREP_SESSION_READY") {
		t.Fatal("temporary Windows preparation must verify that the intended admin session signed in")
	}
	if isolated := isolatedValidationNIC("virtio=02:00:00:00:00:01,bridge=vmbr0,firewall=1"); !strings.Contains(isolated, "bridge=vmbr0") || !strings.Contains(isolated, "link_down=1") {
		t.Fatalf("temporary Windows validation NIC should preserve its hardware config and disable its link: %q", isolated)
	}
	if isolated := isolatedValidationNIC("virtio=02:00:00:00:00:01,link_down=0"); strings.Contains(isolated, "link_down=0") || !strings.Contains(isolated, "link_down=1") {
		t.Fatalf("temporary Windows validation NIC should override an existing active link: %q", isolated)
	}
	for _, imageState := range []string{"IMAGE_STATE_UNDEPLOYABLE", "IMAGE_STATE_GENERALIZE_RESEAL_TO_OOBE", "IMAGE_STATE_SPECIALIZE_RESEAL_TO_OOBE"} {
		if windowsFirstBootStateValidated(imageState) {
			t.Errorf("Windows first-boot state %q was incorrectly accepted", imageState)
		}
	}
	var sysprepCommand string = windowsInteractiveSysprepCommand()
	if !strings.Contains(sysprepCommand, "-LogonType Interactive") || !strings.Contains(sysprepCommand, "IMAGE_STATE_GENERALIZE_RESEAL_TO_OOBE") || !strings.Contains(sysprepCommand, "/unattend:C:\\Windows\\Panther\\unattend.xml") || !strings.Contains(sysprepCommand, "[guid]::NewGuid()") || !strings.Contains(sysprepCommand, "$taskInfo.LastRunTime -ge $startedAt") || strings.Contains(sysprepCommand, "Start-Process -FilePath \"$env:SystemRoot\\System32\\Sysprep\\Sysprep.exe\"") {
		t.Fatal("Windows sealing must wait for this run's interactive Sysprep task and verify the reseal state")
	}
	var answerFile []byte
	var err error
	if answerFile, err = windowsUnattendFile(); err != nil {
		t.Fatalf("create Windows unattended setup answer file: %v", err)
	}
	var answer string = string(answerFile)
	var decoder *xml.Decoder = xml.NewDecoder(strings.NewReader(answer))
	for {
		if _, err = decoder.Token(); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("Windows unattended setup answer file is not valid XML: %v", err)
		}
	}
	for _, setting := range []string{"<HideWirelessSetupInOOBE>true</HideWirelessSetupInOOBE>", "<HideOnlineAccountScreens>true</HideOnlineAccountScreens>", "<HideLocalAccountScreen>true</HideLocalAccountScreen>", "BypassNRO", "SkipMachineOOBE", "<Name>ogadmin</Name>", "<Group>Administrators</Group>"} {
		if !strings.Contains(answer, setting) {
			t.Errorf("Windows unattended setup answer file is missing %s", setting)
		}
	}
	if strings.Contains(answer, "<SkipMachineOOBE>") || strings.Contains(answer, "SkipUserOOBE") {
		t.Fatal("Windows unattended setup should apply the tested OOBE bypass during specialize, not use deprecated answer-file OOBE flags")
	}
	if !strings.Contains(windowsBitLockerDisableCommand, "manage-bde.exe") || !strings.Contains(windowsBitLockerDisableCommand, "-off $env:SystemDrive") || !strings.Contains(windowsBitLockerStatusCommand, "Win32_EncryptableVolume") {
		t.Fatal("Windows preparation must decrypt and inspect only the OS volume before Sysprep")
	}
	if !strings.Contains(windowsBitLockerStatusCommand, "Test-Path -LiteralPath $manageBde") || !strings.Contains(windowsBitLockerStatusCommand, "Get-Service -Name 'BDESVC'") || !strings.Contains(windowsBitLockerStatusCommand, "refusing to assume the OS volume is decrypted") {
		t.Fatal("a missing Windows BitLocker provider should be accepted only when its service and tools are also absent")
	}
	for _, status := range []struct {
		value      string
		state      string
		percentage int
	}{{value: "FullyDecrypted|0", state: "FullyDecrypted", percentage: 0}, {value: "DecryptionInProgress|63", state: "DecryptionInProgress", percentage: 63}, {value: "DecryptionInProgress|6385", state: "DecryptionInProgress", percentage: 64}, {value: "FullyEncrypted|10000", state: "FullyEncrypted", percentage: 100}} {
		var state string
		var percentage int
		var err error
		if state, percentage, err = parseWindowsBitLockerStatus(status.value); err != nil || state != status.state || percentage != status.percentage {
			t.Errorf("parseWindowsBitLockerStatus(%q) = %q, %d, %v", status.value, state, percentage, err)
		}
	}
	if _, _, err := parseWindowsBitLockerStatus("unexpected"); err == nil {
		t.Fatal("malformed BitLocker status was accepted")
	}
	if _, _, err := parseWindowsBitLockerStatus("DecryptionInProgress|10001"); err == nil {
		t.Fatal("out-of-range BitLocker encryption percentage was accepted")
	}
	var firstBootErr error = windowsFirstBootValidationError("IMAGE_STATE_UNDEPLOYABLE", nil, "setuperr.log: CryptoSysPrep_Specialize failed")
	if !strings.Contains(firstBootErr.Error(), "CryptoSysPrep_Specialize failed") {
		t.Fatal("Windows first-boot errors should include the captured setup diagnostics")
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
	if err := ValidateTemplatePreparationScripts("windows", map[string]string{"windows": "powershell script"}); err != nil {
		t.Fatalf("valid Windows prep bundle rejected: %v", err)
	}
	if err := ValidateTemplatePreparationScripts("bsd", map[string]string{"freebsd": "#!/bin/sh"}); err != nil {
		t.Fatalf("valid FreeBSD prep bundle rejected: %v", err)
	}
	if err := ValidateTemplatePreparationScripts("windows", map[string]string{"windows-common": "common", "arbitrary": "code"}); err == nil {
		t.Fatal("mismatched Windows prep bundle was accepted")
	}
}

// TestParseFreeBSDGuestMetadataUsesQGAExecFallback verifies metadata emitted by FreeBSD's uname commands.
func TestParseFreeBSDGuestMetadataUsesQGAExecFallback(t *testing.T) {
	var result PreflightResult
	if err := parseFreeBSDGuestMetadata("freebsd\npfSense\n2.8.0-RELEASE\namd64\n", &result); err != nil {
		t.Fatalf("parse pfSense guest metadata: %v", err)
	}
	if result.GuestOSID != "freebsd" || result.GuestOSName != "pfSense" || result.GuestOSVersion != "2.8.0-RELEASE" || result.GuestArchitecture != "x86_64" {
		t.Fatalf("unexpected detected FreeBSD metadata: %#v", result)
	}
	if err := parseFreeBSDGuestMetadata("linux\nFedora\n44\nx86_64", &result); err == nil {
		t.Fatal("non-FreeBSD guest metadata was accepted by the FreeBSD fallback")
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
