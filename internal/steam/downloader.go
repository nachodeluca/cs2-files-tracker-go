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

type DownloadFileResult struct {
	Path       string
	Downloaded bool
	Size       uint64
}

func (c *Client) DownloadFile(
	ctx context.Context,
	manifest *Manifest,
	name string,
) (string, error) {
	result, err := c.DownloadFileWithStatus(ctx, manifest, name)
	return result.Path, err
}

func (c *Client) DownloadFileWithStatus(
	ctx context.Context,
	manifest *Manifest,
	name string,
) (DownloadFileResult, error) {
	if c == nil || c.Client == nil {
		return DownloadFileResult{}, errors.New("steam client is not initialized")
	}

	if ctx == nil {
		return DownloadFileResult{}, errors.New("context cannot be nil")
	}

	if manifest == nil {
		return DownloadFileResult{}, errors.New("manifest cannot be nil")
	}

	if manifest.DepotID != 0 && manifest.DepotID != c.DepotId {
		return DownloadFileResult{}, fmt.Errorf(
			"manifest belongs to depot %d, expected depot %d",
			manifest.DepotID,
			c.DepotId,
		)
	}

	requestedName, err := cleanManifestPath(name)
	if err != nil {
		return DownloadFileResult{}, fmt.Errorf("invalid file name: %w", err)
	}

	manifestFile, err := findManifestFile(manifest, requestedName)
	if err != nil {
		return DownloadFileResult{}, err
	}

	if manifestFile.Flags&0x40 != 0 {
		return DownloadFileResult{}, fmt.Errorf("%q is a directory", requestedName)
	}

	if manifestFile.Size > math.MaxInt64 {
		return DownloadFileResult{}, fmt.Errorf(
			"file %q is too large: %d bytes",
			manifestFile.Filename,
			manifestFile.Size,
		)
	}

	relativePath, err := cleanManifestPath(manifestFile.Filename)
	if err != nil {
		return DownloadFileResult{}, fmt.Errorf(
			"manifest contains an invalid path %q: %w",
			manifestFile.Filename,
			err,
		)
	}

	outputPath := filepath.Join(
		c.TempDir,
		filepath.FromSlash(relativePath),
	)

	matches, err := existingFileMatches(
		ctx,
		outputPath,
		manifestFile.Size,
		manifestFile.SHAContent,
	)
	if err != nil {
		return DownloadFileResult{}, fmt.Errorf(
			"verify existing %q: %w",
			manifestFile.Filename,
			err,
		)
	}
	if matches {
		return DownloadFileResult{
			Path: outputPath,
			Size: manifestFile.Size,
		}, nil
	}

	depotKey, cdnClient, err := c.downloadSession(ctx)
	if err != nil {
		return DownloadFileResult{}, err
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return DownloadFileResult{}, fmt.Errorf(
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
		return DownloadFileResult{}, fmt.Errorf("create temporary file: %w", err)
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
		return DownloadFileResult{}, fmt.Errorf(
			"truncate temporary file to %d bytes: %w",
			manifestFile.Size,
			err,
		)
	}

	err = cdnClient.DownloadChunksParallel(
		ctx,
		c.DepotId,
		depotKey,
		manifestFile.Chunks,
		c.chunkWorkers(),
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
		return DownloadFileResult{}, fmt.Errorf(
			"download %q: %w",
			manifestFile.Filename,
			err,
		)
	}

	if err := tempFile.Sync(); err != nil {
		return DownloadFileResult{}, fmt.Errorf("sync downloaded file: %w", err)
	}

	if len(manifestFile.SHAContent) > 0 {
		if err := verifyFileSHA1(tempFile, manifestFile.SHAContent); err != nil {
			return DownloadFileResult{}, fmt.Errorf(
				"verify %q: %w",
				manifestFile.Filename,
				err,
			)
		}
	}

	if err := tempFile.Close(); err != nil {
		return DownloadFileResult{}, fmt.Errorf("close downloaded file: %w", err)
	}

	if err := os.Chmod(tempPath, 0o644); err != nil {
		return DownloadFileResult{}, fmt.Errorf("set file permissions: %w", err)
	}

	if err := os.Remove(outputPath); err != nil && !os.IsNotExist(err) {
		return DownloadFileResult{}, fmt.Errorf(
			"remove previous file %q: %w",
			outputPath,
			err,
		)
	}

	if err := os.Rename(tempPath, outputPath); err != nil {
		return DownloadFileResult{}, fmt.Errorf(
			"move downloaded file to %q: %w",
			outputPath,
			err,
		)
	}

	completed = true

	return DownloadFileResult{
		Path:       outputPath,
		Downloaded: true,
		Size:       manifestFile.Size,
	}, nil
}

func (c *Client) downloadSession(
	ctx context.Context,
) ([]byte, *gamejanitor.CDNClient, error) {
	c.downloadSessionMu.Lock()
	defer c.downloadSessionMu.Unlock()

	if len(c.depotKey) > 0 && c.cdnClient != nil {
		return c.depotKey, c.cdnClient, nil
	}

	depotKey, err := c.Client.GetDepotDecryptionKey(
		ctx,
		c.DepotId,
		c.AppId,
	)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"get decryption key for depot %d: %w",
			c.DepotId,
			err,
		)
	}
	if len(depotKey) == 0 {
		return nil, nil, fmt.Errorf(
			"Steam returned an empty decryption key for depot %d",
			c.DepotId,
		)
	}

	cdnHosts, err := c.Client.GetCDNServers(ctx, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("get CDN servers: %w", err)
	}
	if len(cdnHosts) == 0 {
		return nil, nil, errors.New("Steam returned no CDN servers")
	}

	logger := slog.New(
		slog.NewTextHandler(io.Discard, nil),
	)
	c.depotKey = depotKey
	c.cdnClient = gamejanitor.NewCDNClient(logger, cdnHosts)

	return c.depotKey, c.cdnClient, nil
}

func (c *Client) chunkWorkers() int {
	if c.ChunkWorkers <= 0 {
		return 16
	}
	if c.ChunkWorkers > 64 {
		return 64
	}
	return c.ChunkWorkers
}

func existingFileMatches(
	ctx context.Context,
	path string,
	expectedSize uint64,
	expectedSHA1 []byte,
) (bool, error) {
	if len(expectedSHA1) != sha1.Size {
		return false, nil
	}

	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Size() < 0 ||
		uint64(info.Size()) != expectedSize {
		return false, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()

	hash := sha1.New()
	buffer := make([]byte, 1024*1024)
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}

		n, readErr := file.Read(buffer)
		if n > 0 {
			if _, err := hash.Write(buffer[:n]); err != nil {
				return false, err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return false, readErr
		}
	}

	return bytes.Equal(hash.Sum(nil), expectedSHA1), nil
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
		name := fmt.Sprintf("game/csgo/pak01_%03d.vpk", index)

		if _, err := c.DownloadFile(ctx, manifest, name); err != nil {
			return fmt.Errorf("download %s: %w", name, err)
		}
	}

	return nil
}
