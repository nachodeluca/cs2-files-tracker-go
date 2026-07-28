package steam

import (
	"bytes"
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"

	gamejanitor "github.com/warsmite/gamejanitor/steam"
)

func (c *Client) DownloadFile(
	ctx context.Context,
	manifest *Manifest,
	name string,
) (string, error) {
	if c == nil || c.Client == nil {
		return "", errors.New("steam client is not initialized")
	}

	if ctx == nil {
		return "", errors.New("context cannot be nil")
	}

	if manifest == nil {
		return "", errors.New("manifest cannot be nil")
	}

	if manifest.DepotID != 0 && manifest.DepotID != c.DepotId {
		return "", fmt.Errorf(
			"manifest belongs to depot %d, expected depot %d",
			manifest.DepotID,
			c.DepotId,
		)
	}

	requestedName, err := cleanManifestPath(name)
	if err != nil {
		return "", fmt.Errorf("invalid file name: %w", err)
	}

	manifestFile, err := findManifestFile(manifest, requestedName)
	if err != nil {
		return "", err
	}

	if manifestFile.Flags&0x40 != 0 {
		return "", fmt.Errorf("%q is a directory", requestedName)
	}

	if manifestFile.Size > math.MaxInt64 {
		return "", fmt.Errorf(
			"file %q is too large: %d bytes",
			manifestFile.Filename,
			manifestFile.Size,
		)
	}

	depotKey, err := c.Client.GetDepotDecryptionKey(
		ctx,
		c.DepotId,
		c.AppId,
	)
	if err != nil {
		return "", fmt.Errorf(
			"get decryption key for depot %d: %w",
			c.DepotId,
			err,
		)
	}

	if len(depotKey) == 0 {
		return "", fmt.Errorf(
			"Steam returned an empty decryption key for depot %d",
			c.DepotId,
		)
	}

	cdnHosts, err := c.Client.GetCDNServers(ctx, 0)
	if err != nil {
		return "", fmt.Errorf("get CDN servers: %w", err)
	}

	if len(cdnHosts) == 0 {
		return "", errors.New("Steam returned no CDN servers")
	}

	relativePath, err := cleanManifestPath(manifestFile.Filename)
	if err != nil {
		return "", fmt.Errorf(
			"manifest contains an invalid path %q: %w",
			manifestFile.Filename,
			err,
		)
	}

	outputPath := filepath.Join(
		c.TempDir,
		filepath.FromSlash(relativePath),
	)

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return "", fmt.Errorf(
			"create directory for %q: %w",
			outputPath,
			err,
		)
	}

	tempFile, err := os.CreateTemp(
		filepath.Dir(outputPath),
		filepath.Base(outputPath)+".*.part",
	)
	if err != nil {
		return "", fmt.Errorf("create temporary file: %w", err)
	}

	tempPath := tempFile.Name()
	completed := false

	defer func() {
		_ = tempFile.Close()

		if !completed {
			_ = os.Remove(tempPath)
		}
	}()

	if err := tempFile.Truncate(int64(manifestFile.Size)); err != nil {
		return "", fmt.Errorf(
			"truncate temporary file to %d bytes: %w",
			manifestFile.Size,
			err,
		)
	}

	logger := slog.New(
		slog.NewTextHandler(io.Discard, nil),
	)

	cdnClient := gamejanitor.NewCDNClient(logger, cdnHosts)

	const workers = 8

	err = cdnClient.DownloadChunksParallel(
		ctx,
		c.DepotId,
		depotKey,
		manifestFile.Chunks,
		workers,
		func(chunk gamejanitor.ManifestChunk, data []byte) error {
			written, err := tempFile.WriteAt(
				data,
				int64(chunk.Offset),
			)
			if err != nil {
				return fmt.Errorf(
					"write chunk %s at offset %d: %w",
					chunk.ChunkIDHex(),
					chunk.Offset,
					err,
				)
			}

			if written != len(data) {
				return fmt.Errorf(
					"write chunk %s: wrote %d of %d bytes",
					chunk.ChunkIDHex(),
					written,
					len(data),
				)
			}

			return nil
		},
	)
	if err != nil {
		return "", fmt.Errorf(
			"download %q: %w",
			manifestFile.Filename,
			err,
		)
	}

	if err := tempFile.Sync(); err != nil {
		return "", fmt.Errorf("sync downloaded file: %w", err)
	}

	if len(manifestFile.SHAContent) > 0 {
		if err := verifyFileSHA1(tempFile, manifestFile.SHAContent); err != nil {
			return "", fmt.Errorf(
				"verify %q: %w",
				manifestFile.Filename,
				err,
			)
		}
	}

	if err := tempFile.Close(); err != nil {
		return "", fmt.Errorf("close downloaded file: %w", err)
	}

	if err := os.Chmod(tempPath, 0o644); err != nil {
		return "", fmt.Errorf("set file permissions: %w", err)
	}

	if err := os.Remove(outputPath); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf(
			"remove previous file %q: %w",
			outputPath,
			err,
		)
	}

	if err := os.Rename(tempPath, outputPath); err != nil {
		return "", fmt.Errorf(
			"move downloaded file to %q: %w",
			outputPath,
			err,
		)
	}

	completed = true

	return outputPath, nil
}

func findManifestFile(
	manifest *Manifest,
	requestedName string,
) (*gamejanitor.ManifestFile, error) {
	for i := range manifest.Files {
		filename, err := cleanManifestPath(manifest.Files[i].Filename)
		if err != nil {
			continue
		}

		if filename == requestedName {
			return &manifest.Files[i], nil
		}
	}

	for i := range manifest.Files {
		filename, err := cleanManifestPath(manifest.Files[i].Filename)
		if err != nil {
			continue
		}

		if strings.EqualFold(filename, requestedName) {
			return &manifest.Files[i], nil
		}
	}

	return nil, fmt.Errorf(
		"file %q was not found in manifest %d",
		requestedName,
		manifest.ManifestID,
	)
}

func cleanManifestPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\\", "/")

	if value == "" {
		return "", errors.New("path cannot be empty")
	}

	cleaned := path.Clean(value)

	if cleaned == "." ||
		cleaned == ".." ||
		strings.HasPrefix(cleaned, "/") ||
		strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("unsafe relative path %q", value)
	}

	if len(cleaned) >= 2 && cleaned[1] == ':' {
		return "", fmt.Errorf("absolute Windows path is not allowed: %q", value)
	}

	return cleaned, nil
}

func verifyFileSHA1(file *os.File, expected []byte) error {
	if len(expected) != sha1.Size {
		return fmt.Errorf(
			"invalid expected SHA-1 length: got %d, expected %d",
			len(expected),
			sha1.Size,
		)
	}

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek to beginning: %w", err)
	}

	hash := sha1.New()

	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("calculate SHA-1: %w", err)
	}

	actual := hash.Sum(nil)

	if !bytes.Equal(actual, expected) {
		return fmt.Errorf(
			"SHA-1 mismatch: got %x, expected %x",
			actual,
			expected,
		)
	}

	return nil
}

func (c *Client) DownloadArchives(ctx context.Context, manifest *Manifest, indices []uint16) error {
	for _, index := range indices {
		name := fmt.Sprintf("game/csgo/pak01_%03d", index)

		if _, err := c.DownloadFile(ctx, manifest, name); err != nil {
			return fmt.Errorf("download %s: %d", name, err)
		}
	}

	return nil
}
