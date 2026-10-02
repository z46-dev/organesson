package proxmox

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/z46-dev/organesson/backend/config"
)

func TestValidateGuestArtifactArchiveRejectsUnsafeArchives(t *testing.T) {
	var validArchive []byte = buildGuestArtifactArchive(t, []artifactTestEntry{{name: "entrypoint.sh", mode: 0755, body: []byte("exit 0\n"), kind: tar.TypeReg}})
	var digest [sha256.Size]byte = sha256.Sum256(validArchive)
	var validRequest GuestArtifactRequest = GuestArtifactRequest{
		Node: "pve1", VMID: "901", VMOperationKey: "og-test", Entrypoint: "entrypoint.sh",
		SHA256: hex.EncodeToString(digest[:]), Archive: validArchive,
	}
	var files []GuestArtifactFile
	var err error
	if files, err = validateGuestArtifact(validRequest); err != nil || len(files) != 1 || !files[0].Executable {
		t.Fatalf("valid artifact rejected: files=%#v err=%v", files, err)
	}
	validRequest.SHA256 = strings.Repeat("0", 64)
	if err = ValidateGuestArtifactArchive(validRequest); err == nil {
		t.Fatal("artifact with a mismatched digest was accepted")
	}
	for _, testCase := range []struct {
		name    string
		entries []artifactTestEntry
		entry   string
	}{
		{name: "traversal", entries: []artifactTestEntry{{name: "../outside", mode: 0644, body: []byte("x"), kind: tar.TypeReg}}, entry: "../outside"},
		{name: "absolute", entries: []artifactTestEntry{{name: "/outside", mode: 0644, body: []byte("x"), kind: tar.TypeReg}}, entry: "/outside"},
		{name: "symbolic link", entries: []artifactTestEntry{{name: "entrypoint.sh", mode: 0777, body: []byte("elsewhere"), kind: tar.TypeSymlink}}, entry: "entrypoint.sh"},
		{name: "duplicate", entries: []artifactTestEntry{{name: "entrypoint.sh", mode: 0644, body: []byte("a"), kind: tar.TypeReg}, {name: "entrypoint.sh", mode: 0644, body: []byte("b"), kind: tar.TypeReg}}, entry: "entrypoint.sh"},
		{name: "file parent conflict", entries: []artifactTestEntry{{name: "parent", mode: 0644, body: []byte("a"), kind: tar.TypeReg}, {name: "parent/child", mode: 0644, body: []byte("b"), kind: tar.TypeReg}}, entry: "parent"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var archive []byte = buildGuestArtifactArchive(t, testCase.entries)
			var hash [sha256.Size]byte = sha256.Sum256(archive)
			var request GuestArtifactRequest = validRequest
			request.Archive = archive
			request.SHA256 = hex.EncodeToString(hash[:])
			request.Entrypoint = testCase.entry
			if err := ValidateGuestArtifactArchive(request); err == nil {
				t.Fatal("unsafe artifact was accepted")
			}
		})
	}
}

func TestGuestArtifactDriverTransfersFilesAndAlwaysCleansWorkspace(t *testing.T) {
	var runExitCode int = 0
	var runnerPID int
	var writes []map[string]any
	var commands [][]string
	var fakePVE *httptest.Server
	fakePVE = httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "PVEAPIToken=test!organesson=secret" {
			t.Errorf("Proxmox request is missing its API token")
		}
		var body map[string]any
		if request.Body != nil && request.Method != http.MethodGet {
			_ = json.NewDecoder(request.Body).Decode(&body)
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/pve1/status":
			writePVEData(response, map[string]any{"node": "pve1", "status": "online"})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/pve1/qemu/901/status/current":
			writePVEData(response, map[string]any{"vmid": 901, "name": "artifact-smoke", "status": "running"})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/pve1/qemu/901/config":
			writePVEData(response, map[string]any{"name": "artifact-smoke", "description": "Organesson managed resource og-test"})
		case request.Method == http.MethodPost && request.URL.Path == "/api2/json/nodes/pve1/qemu/901/agent/ping":
			writePVEData(response, nil)
		case request.Method == http.MethodPost && request.URL.Path == "/api2/json/nodes/pve1/qemu/901/agent/file-write":
			writes = append(writes, body)
			writePVEData(response, nil)
		case request.Method == http.MethodPost && request.URL.Path == "/api2/json/nodes/pve1/qemu/901/agent/exec":
			var command []string
			var rawCommand []any = body["command"].([]any)
			for _, argument := range rawCommand {
				command = append(command, argument.(string))
			}
			commands = append(commands, command)
			if len(command) > 4 && command[4] == "/usr/bin/bash" {
				runnerPID = len(commands)
			}
			writePVEData(response, map[string]any{"pid": len(commands)})
		case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/api2/json/nodes/pve1/qemu/901/agent/exec-status"):
			var exitCode int
			if request.URL.Query().Get("pid") == strconv.Itoa(runnerPID) {
				exitCode = runExitCode
			}
			writePVEData(response, map[string]any{"exited": 1, "exitcode": exitCode})
		default:
			t.Errorf("unexpected fake PVE request: %s %s", request.Method, request.URL.String())
			writePVEData(response, nil)
		}
	}))
	defer fakePVE.Close()
	var service *Service = New(config.ProxmoxConfiguration{
		APIURL: fakePVE.URL + "/api2/json", APITokenID: "test!organesson", APITokenSecret: "secret", InsecureSkipVerify: true,
	})
	var archive []byte = buildGuestArtifactArchive(t, []artifactTestEntry{{name: "entrypoint.sh", mode: 0755, body: []byte("exit 0\n"), kind: tar.TypeReg}})
	var digest [sha256.Size]byte = sha256.Sum256(archive)
	var request GuestArtifactRequest = GuestArtifactRequest{
		Node: "pve1", VMID: "901", VMOperationKey: "og-test", Entrypoint: "entrypoint.sh",
		SHA256: hex.EncodeToString(digest[:]), Archive: archive,
	}
	var result GuestArtifactResult
	var err error
	if result, err = service.ExecuteGuestArtifact(context.Background(), request); err != nil {
		t.Fatalf("execute artifact: %v", err)
	}
	if result.Status != "succeeded" || result.SHA256 != request.SHA256 || len(writes) != 3 || len(commands) != 3 {
		t.Fatalf("unexpected execution evidence: result=%#v writes=%d commands=%#v", result, len(writes), commands)
	}
	if !strings.Contains(commands[1][2], "/usr/libexec/qemu-ga/fsfreeze-hook.d/organesson-qga-exec") || commands[1][4] != "/usr/bin/bash" || commands[1][7] != "entrypoint.sh" {
		t.Fatalf("artifact entrypoint was not run using the privileged guest wrapper: %#v", commands[1])
	}
	if !strings.Contains(string(decodeAgentWriteContent(t, writes[1])), "entrypoint.sh") || !strings.Contains(string(decodeAgentWriteContent(t, writes[2])), "trap cleanup EXIT") {
		t.Fatal("guest delivery did not include an entrypoint manifest and self-cleaning runner")
	}
	if !strings.Contains(commands[1][6], "/run/organesson-artifact-") || !strings.Contains(commands[0][8], "/run/organesson-artifact-") {
		t.Fatalf("guest temporary workspace was not isolated under /run: %#v", commands)
	}
	if !strings.Contains(commands[1][2], "exec \"$@\"") {
		t.Fatalf("SELinux dispatch wrapper does not preserve the root command: %#v", commands[1])
	}
	runExitCode = 9
	if result, err = service.ExecuteGuestArtifact(context.Background(), request); err == nil {
		t.Fatal("non-zero guest exit status was accepted")
	} else {
		var executionError *GuestArtifactExitError
		if !errors.As(err, &executionError) || executionError.ExitCode != 9 || result.Status != "failed" || result.ExitCode != 9 {
			t.Fatalf("guest failure did not preserve the entrypoint exit code: result=%#v err=%v", result, err)
		}
	}
	if len(commands) != 6 || commands[5][4] != "/usr/bin/rm" || commands[5][5] != "-rf" {
		t.Fatalf("failed execution did not attempt workspace cleanup: commands=%#v", commands)
	}
}

type artifactTestEntry struct {
	name string
	mode int64
	body []byte
	kind byte
}

func buildGuestArtifactArchive(t *testing.T, entries []artifactTestEntry) (archive []byte) {
	t.Helper()
	var buffer bytes.Buffer
	var compressed *gzip.Writer = gzip.NewWriter(&buffer)
	var writer *tar.Writer = tar.NewWriter(compressed)
	for _, entry := range entries {
		var header *tar.Header = &tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.body)), Typeflag: entry.kind, Format: tar.FormatUSTAR}
		if entry.kind == tar.TypeSymlink {
			header.Linkname = string(entry.body)
			header.Size = 0
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if entry.kind == tar.TypeReg || entry.kind == tar.TypeRegA {
			if _, err := writer.Write(entry.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	archive = buffer.Bytes()
	return
}

func decodeAgentWriteContent(t *testing.T, request map[string]any) (content []byte) {
	t.Helper()
	var encoded string
	if encoded, _ = request["content"].(string); encoded == "" {
		t.Fatal("guest file-write did not include content")
	}
	var err error
	if content, err = base64.StdEncoding.DecodeString(encoded); err != nil {
		t.Fatal(err)
	}
	return
}
