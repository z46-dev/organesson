package organesson

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

const (
	maxArtifactFiles       int   = 128
	maxArtifactSourceBytes int64 = 16 * 1024 * 1024
)

type artifactPackage struct {
	Archive   []byte
	SHA256    string
	FileCount int
	SizeBytes int64
}

type artifactFile struct {
	Path     string
	Info     fs.FileInfo
	Contents []byte
}

// packageArtifact creates a deterministic tar.gz while rejecting unsafe filesystem entries.
func packageArtifact(sourceDirectory string, entrypoint string) (artifact artifactPackage, err error) {
	return packageArtifactWithFiles(sourceDirectory, entrypoint, nil)
}

// packageArtifactWithFiles adds generated regular files to a deterministic local artifact package.
func packageArtifactWithFiles(sourceDirectory string, entrypoint string, inlineFiles map[string]string) (artifact artifactPackage, err error) {
	var (
		root       string
		files      []artifactFile
		totalBytes int64
		entryFound bool
		archive    strings.Builder
	)

	if sourceDirectory == "" {
		err = errors.New("artifact source directory is required")
		return
	}
	if entrypoint == "" || len(entrypoint) > 512 || filepath.IsAbs(entrypoint) || strings.Contains(entrypoint, "\\") || strings.ContainsAny(entrypoint, "\x00\r\n\t") || filepath.Clean(entrypoint) != entrypoint || entrypoint == "." || entrypoint == ".." || strings.HasPrefix(entrypoint, "../") {
		err = errors.New("artifact entrypoint must be a clean relative path within the source directory")
		return
	}
	if root, err = filepath.Abs(sourceDirectory); err != nil {
		return
	}
	var rootInfo fs.FileInfo
	if rootInfo, err = os.Stat(root); err != nil {
		err = fmt.Errorf("inspect artifact source directory: %w", err)
		return
	}
	if !rootInfo.IsDir() {
		err = errors.New("artifact source path is not a directory")
		return
	}
	if err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) (walkResult error) {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return
		}
		var relative string
		if relative, walkResult = filepath.Rel(root, path); walkResult != nil {
			return
		}
		if filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("artifact path %q escapes its source directory", relative)
		}
		if entry.IsDir() {
			return
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("artifact source contains unsupported symbolic link %q", relative)
		}
		var info fs.FileInfo
		if info, walkResult = entry.Info(); walkResult != nil {
			return
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("artifact source contains unsupported non-regular file %q", relative)
		}
		if len(files) >= maxArtifactFiles {
			return fmt.Errorf("artifact exceeds the limit of %d files", maxArtifactFiles)
		}
		if info.Size() < 0 || info.Size() > maxArtifactSourceBytes-totalBytes {
			return fmt.Errorf("artifact source exceeds the %d-byte uncompressed size limit", maxArtifactSourceBytes)
		}
		totalBytes += info.Size()
		relative = filepath.ToSlash(relative)
		if len(relative) > 512 || strings.Contains(relative, "\\") || strings.ContainsAny(relative, "\x00\r\n\t") {
			return fmt.Errorf("artifact path %q contains unsupported characters or exceeds 512 bytes", relative)
		}
		if relative == entrypoint {
			entryFound = true
		}
		files = append(files, artifactFile{Path: relative, Info: info})
		return
	}); err != nil {
		return
	}
	for filePath, contents := range inlineFiles {
		if filePath == "" || filePath == "." || strings.Contains(filePath, "\\") || strings.ContainsAny(filePath, "\x00\r\n\t") || path.IsAbs(filePath) || path.Clean(filePath) != filePath || strings.HasPrefix(filePath, "../") || len(filePath) > 512 {
			err = fmt.Errorf("generated artifact path %q is unsafe", filePath)
			return
		}
		if filePath == entrypoint {
			err = errors.New("generated artifact files cannot replace the entrypoint")
			return
		}
		if _, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(filePath))); statErr == nil || !errors.Is(statErr, os.ErrNotExist) {
			if statErr != nil {
				err = statErr
			} else {
				err = fmt.Errorf("generated artifact file %q conflicts with a source file or directory", filePath)
			}
			return
		}
		for _, existing := range files {
			if existing.Path == filePath || strings.HasPrefix(existing.Path, filePath+"/") || strings.HasPrefix(filePath, existing.Path+"/") {
				err = fmt.Errorf("generated artifact file %q conflicts with a source path", filePath)
				return
			}
		}
		if len(files) >= maxArtifactFiles || int64(len(contents)) > maxArtifactSourceBytes-totalBytes {
			err = fmt.Errorf("generated artifact files exceed the %d-file or %d-byte package limit", maxArtifactFiles, maxArtifactSourceBytes)
			return
		}
		totalBytes += int64(len(contents))
		files = append(files, artifactFile{Path: filePath, Contents: []byte(contents)})
	}
	if !entryFound {
		err = fmt.Errorf("artifact entrypoint %q does not name a regular file in the source directory", entrypoint)
		return
	}
	sort.Slice(files, func(leftIndex int, rightIndex int) (less bool) {
		less = files[leftIndex].Path < files[rightIndex].Path
		return
	})

	var gzipWriter *gzip.Writer = gzip.NewWriter(&archive)
	gzipWriter.Header.ModTime = time.Unix(0, 0)
	gzipWriter.Header.OS = 255
	var tarWriter *tar.Writer = tar.NewWriter(gzipWriter)
	for _, file := range files {
		var (
			mode int64 = 0644
			size int64 = int64(len(file.Contents))
		)
		if file.Info != nil {
			size = file.Info.Size()
			if file.Info.Mode().Perm()&0111 != 0 {
				mode = 0755
			}
		}
		var header *tar.Header = &tar.Header{
			Name:     file.Path,
			Mode:     mode,
			Size:     size,
			ModTime:  time.Unix(0, 0),
			Typeflag: tar.TypeReg,
			Format:   tar.FormatUSTAR,
		}
		if err = tarWriter.WriteHeader(header); err != nil {
			break
		}
		if file.Info == nil {
			if _, err = tarWriter.Write(file.Contents); err != nil {
				break
			}
			continue
		}
		var source *os.File
		if source, err = os.Open(filepath.Join(root, filepath.FromSlash(file.Path))); err != nil {
			break
		}
		var openedInfo fs.FileInfo
		if openedInfo, err = source.Stat(); err == nil && (!openedInfo.Mode().IsRegular() || !os.SameFile(file.Info, openedInfo)) {
			err = fmt.Errorf("artifact file %q changed while it was being packaged", file.Path)
		}
		var copied int64
		if err == nil {
			copied, err = io.Copy(tarWriter, source)
		}
		if err == nil && copied != file.Info.Size() {
			err = fmt.Errorf("artifact file %q changed while it was being packaged", file.Path)
		}
		var closeErr error
		if closeErr = source.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			break
		}
	}
	if closeErr := tarWriter.Close(); err == nil {
		err = closeErr
	}
	if closeErr := gzipWriter.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return
	}
	artifact.Archive = []byte(archive.String())
	var digest [sha256.Size]byte = sha256.Sum256(artifact.Archive)
	artifact.SHA256 = hex.EncodeToString(digest[:])
	artifact.FileCount = len(files)
	artifact.SizeBytes = int64(len(artifact.Archive))
	return
}

// artifactResource creates a provider-local package manifest without persisting package contents in OpenTofu state.
func artifactResource(fields map[string]*schema.Schema) (resource *schema.Resource) {
	resource = &schema.Resource{
		CreateContext: artifactCreate,
		ReadContext:   artifactRead,
		DeleteContext: artifactDelete,
		CustomizeDiff: artifactCustomizeDiff,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},
		Schema:        fields,
	}
	return
}

// artifactCustomizeDiff fingerprints files during planning without placing their contents in plan or state.
func artifactCustomizeDiff(_ context.Context, data *schema.ResourceDiff, _ interface{}) (err error) {
	var previousDigest any
	previousDigest, _ = data.GetChange("sha256")
	var artifact artifactPackage
	if artifact, err = packageArtifactWithFiles(data.Get("source_directory").(string), data.Get("entrypoint").(string), artifactInlineFiles(data.Get("inline_files"))); err != nil {
		return
	}
	if err = data.SetNew("file_count", artifact.FileCount); err != nil {
		return
	}
	if err = data.SetNew("sha256", artifact.SHA256); err != nil {
		return
	}
	if err = data.SetNew("size_bytes", int(artifact.SizeBytes)); err != nil {
		return
	}
	var summary string = fmt.Sprintf("packaged %d files (%d bytes, sha256 %s)", artifact.FileCount, artifact.SizeBytes, artifact.SHA256)
	err = data.SetNew("summary", summary)
	if err != nil {
		return
	}
	if oldDigest, ok := previousDigest.(string); ok && oldDigest != "" && oldDigest != artifact.SHA256 {
		err = data.ForceNew("sha256")
	}
	return
}

// artifactCreate packages local sources and records only the digest and package metadata.
func artifactCreate(_ context.Context, data *schema.ResourceData, _ interface{}) (diagnostics diag.Diagnostics) {
	var artifact artifactPackage
	var err error
	if artifact, err = packageArtifactWithFiles(data.Get("source_directory").(string), data.Get("entrypoint").(string), artifactInlineFiles(data.Get("inline_files"))); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	if err = setArtifactState(data, artifact); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	data.SetId(localResourceID("artifact", data.Get("source_directory").(string)+"\x00"+data.Get("entrypoint").(string)+"\x00"+data.Get("deployment_id").(string)))
	return
}

// artifactRead recalculates the manifest so local source edits are visible after refresh.
func artifactRead(_ context.Context, data *schema.ResourceData, _ interface{}) (diagnostics diag.Diagnostics) {
	var artifact artifactPackage
	var err error
	if artifact, err = packageArtifactWithFiles(data.Get("source_directory").(string), data.Get("entrypoint").(string), artifactInlineFiles(data.Get("inline_files"))); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	if err = setArtifactState(data, artifact); err != nil {
		diagnostics = diag.FromErr(err)
	}
	return
}

// artifactDelete forgets the local package manifest; no remote or guest files are created yet.
func artifactDelete(_ context.Context, data *schema.ResourceData, _ interface{}) (diagnostics diag.Diagnostics) {
	data.SetId("")
	return
}

// setArtifactState records package metadata without copying the archive into state.
func setArtifactState(data *schema.ResourceData, artifact artifactPackage) (err error) {
	var summary string = fmt.Sprintf("packaged %d files (%d bytes, sha256 %s)", artifact.FileCount, artifact.SizeBytes, artifact.SHA256)
	if err = data.Set("file_count", artifact.FileCount); err != nil {
		return
	}
	if err = data.Set("sha256", artifact.SHA256); err != nil {
		return
	}
	if err = data.Set("size_bytes", artifact.SizeBytes); err != nil {
		return
	}
	if err = data.Set("summary", summary); err != nil {
		return
	}
	return
}
