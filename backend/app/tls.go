package app

import (
	"os"
	"path/filepath"
	"strings"
)

var tlsCandidates []struct {
	cert string
	key  string
} = []struct {
	cert string
	key  string
}{
	{"fullchain.pem", "privkey.pem"},
	{"cert.pem", "key.pem"},
	{"tls.crt", "tls.key"},
	{"server.crt", "server.key"},
	{"webserver.crt", "webserver.key"},
}

func fExists(path string) (exists bool) {
	var (
		info os.FileInfo
		err  error
	)

	if info, err = os.Stat(path); err == nil {
		exists = !info.IsDir()
	}

	return
}

func discoverTLSKeys(dir string) (certPath, keyPath string, found bool) {
	found = false

	for _, c := range tlsCandidates {
		certPath = filepath.Join(dir, c.cert)
		keyPath = filepath.Join(dir, c.key)

		if fExists(certPath) && fExists(keyPath) {
			found = true
			return
		}
	}

	var (
		crtFiles, keyFiles []string
		entries            []os.DirEntry
		err                error
	)

	if entries, err = os.ReadDir(dir); err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		var name string = entry.Name()

		if strings.HasSuffix(name, ".crt") || strings.HasSuffix(name, ".pem") {
			crtFiles = append(crtFiles, name)
		} else if strings.HasSuffix(name, ".key") {
			keyFiles = append(keyFiles, name)
		}
	}

	if len(crtFiles) == 1 && len(keyFiles) == 1 {
		certPath = filepath.Join(dir, crtFiles[0])
		keyPath = filepath.Join(dir, keyFiles[0])
		found = true
	}

	return
}
