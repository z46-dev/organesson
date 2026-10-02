package organesson

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func TestPackageArtifactIsDeterministicAndContainsSourceFiles(t *testing.T) {
	var sourceDirectory string = t.TempDir()
	var entrypointPath string = filepath.Join(sourceDirectory, "entrypoint.sh")
	if err := os.WriteFile(entrypointPath, []byte("#!/bin/sh\necho ready\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(sourceDirectory, "scripts"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDirectory, "scripts", "check.sh"), []byte("echo checked\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var first artifactPackage
	var err error
	if first, err = packageArtifact(sourceDirectory, "entrypoint.sh"); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(entrypointPath, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	var second artifactPackage
	if second, err = packageArtifact(sourceDirectory, "entrypoint.sh"); err != nil {
		t.Fatal(err)
	}
	if first.SHA256 != second.SHA256 || !bytes.Equal(first.Archive, second.Archive) {
		t.Fatal("artifact archive changed when only source timestamps changed")
	}
	if first.FileCount != 2 || first.SizeBytes != int64(len(first.Archive)) {
		t.Fatalf("unexpected artifact manifest: files=%d bytes=%d", first.FileCount, first.SizeBytes)
	}
	var compressed *gzip.Reader
	if compressed, err = gzip.NewReader(bytes.NewReader(first.Archive)); err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	var archive *tar.Reader = tar.NewReader(compressed)
	var names []string
	for {
		var header *tar.Header
		if header, err = archive.Next(); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		names = append(names, header.Name)
	}
	if len(names) != 2 || names[0] != "entrypoint.sh" || names[1] != "scripts/check.sh" {
		t.Fatalf("unexpected archive files: %v", names)
	}
}

func TestPackageArtifactRejectsUnsafeSourcesAndEntrypoints(t *testing.T) {
	var sourceDirectory string = t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDirectory, "entrypoint.sh"), []byte("echo safe\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(sourceDirectory, "outside")); err != nil {
		t.Fatal(err)
	}
	if _, err := packageArtifact(sourceDirectory, "entrypoint.sh"); err == nil {
		t.Fatal("expected symbolic link rejection")
	}
	if _, err := packageArtifact(sourceDirectory, "../entrypoint.sh"); err == nil {
		t.Fatal("expected path traversal rejection")
	}
	if _, err := packageArtifact(sourceDirectory, "missing.sh"); err == nil {
		t.Fatal("expected missing entrypoint rejection")
	}
}

func TestPackageArtifactEnforcesFileAndSizeLimits(t *testing.T) {
	var sourceDirectory string = t.TempDir()
	for index := 0; index <= maxArtifactFiles; index++ {
		var name string = fmt.Sprintf("file-%03d", index)
		if err := os.WriteFile(filepath.Join(sourceDirectory, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := packageArtifact(sourceDirectory, "file-000"); err == nil {
		t.Fatal("expected artifact file-count limit rejection")
	}
	var oversizedDirectory string = t.TempDir()
	var oversizedFile *os.File
	var err error
	if oversizedFile, err = os.Create(filepath.Join(oversizedDirectory, "entrypoint.sh")); err != nil {
		t.Fatal(err)
	}
	if err = oversizedFile.Truncate(maxArtifactSourceBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err = oversizedFile.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = packageArtifact(oversizedDirectory, "entrypoint.sh"); err == nil {
		t.Fatal("expected artifact uncompressed-size limit rejection")
	}
}

func TestArtifactResourceRefreshDetectsSourceChanges(t *testing.T) {
	var sourceDirectory string = t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDirectory, "entrypoint.sh"), []byte("echo first\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var resource *schema.Resource = resourceArtifact()
	var data *schema.ResourceData = schema.TestResourceDataRaw(t, resource.Schema, map[string]interface{}{
		"deployment_id":    "1",
		"entrypoint":       "entrypoint.sh",
		"source_directory": sourceDirectory,
	})
	if diagnostics := resource.CreateContext(context.Background(), data, nil); diagnostics.HasError() {
		t.Fatalf("create artifact manifest: %v", diagnostics)
	}
	var firstDigest string = data.Get("sha256").(string)
	if firstDigest == "" || data.Get("file_count").(int) != 1 || data.Get("size_bytes").(int) < 1 {
		t.Fatalf("artifact manifest was not recorded: %#v", data.Get("summary"))
	}
	if err := os.WriteFile(filepath.Join(sourceDirectory, "entrypoint.sh"), []byte("echo second\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if diagnostics := resource.ReadContext(context.Background(), data, nil); diagnostics.HasError() {
		t.Fatalf("refresh artifact manifest: %v", diagnostics)
	}
	if data.Get("sha256").(string) == firstDigest {
		t.Fatal("artifact refresh did not detect changed source content")
	}
}
