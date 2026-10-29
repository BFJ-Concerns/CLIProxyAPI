package managementasset

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"
)

// bundledManagementHTML is the management control panel this fork serves: a
// release build of the panel fork (BFJ-Concerns/Cli-Proxy-API-Management-Center)
// held in this repository, so the panel changes only when the binary does and
// no remote release can replace code that handles the management key.
//
//go:embed panel/management.html
var bundledManagementHTML []byte

// EnsureLatestManagementHTML installs the bundled panel as the local management
// asset, replacing a local copy whose content differs. It never reaches the
// network; the proxy URL and panel repository arguments are accepted for
// upstream's callers and ignored. It returns whether the asset exists afterwards.
func EnsureLatestManagementHTML(_ context.Context, staticDir string, _ string, _ string) bool {
	staticDir = strings.TrimSpace(staticDir)
	if staticDir == "" {
		log.Debug("management asset sync skipped: empty static directory")
		return false
	}
	localPath := filepath.Join(staticDir, managementAssetName)

	_, _, _ = sfGroup.Do(localPath, func() (interface{}, error) {
		sum := sha256.Sum256(bundledManagementHTML)
		bundledHash := hex.EncodeToString(sum[:])

		localHash, errHash := fileSHA256(localPath)
		if errHash == nil && localHash == bundledHash {
			log.Debug("management asset already matches the bundled panel")
			return nil, nil
		}
		if errHash != nil && !errors.Is(errHash, os.ErrNotExist) {
			log.WithError(errHash).Debug("failed to read local management asset hash")
		}

		if errMkdirAll := os.MkdirAll(staticDir, 0o755); errMkdirAll != nil {
			log.WithError(errMkdirAll).Warn("failed to prepare static directory for management asset")
			return nil, nil
		}
		if errWrite := atomicWriteFile(localPath, bundledManagementHTML); errWrite != nil {
			log.WithError(errWrite).Warn("failed to write bundled management asset to disk")
			return nil, nil
		}

		log.Infof("management asset installed from the bundled panel (hash=%s)", bundledHash)
		return nil, nil
	})

	_, errStat := os.Stat(localPath)
	return errStat == nil
}
