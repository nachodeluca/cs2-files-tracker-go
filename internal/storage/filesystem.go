package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func WriteFileAtomic(
	path string,
	data []byte,
	permissions os.FileMode,
) error {
	dir := filepath.Dir(path)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create parent directory: %w", err)
	}

	tempFile, err := os.CreateTemp(
		dir,
		"."+filepath.Base(path)+".*.tmp",
	)
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}

	tempPath := tempFile.Name()
	committed := false

	defer func() {
		_ = tempFile.Close()

		if !committed {
			_ = os.Remove(tempPath)
		}
	}()

	if err := tempFile.Chmod(permissions); err != nil {
		return fmt.Errorf("set temporary file permissions: %w", err)
	}

	if _, err := tempFile.Write(data); err != nil {
		return fmt.Errorf("write temporary file: %w", err)
	}

	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("sync temporary file: %w", err)
	}

	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}

	if err := os.Rename(tempPath, path); err != nil {
		if removeErr := os.Remove(path); removeErr != nil &&
			!errors.Is(removeErr, os.ErrNotExist) {
			return fmt.Errorf(
				"remove previous output file: %w",
				removeErr,
			)
		}

		if renameErr := os.Rename(tempPath, path); renameErr != nil {
			return fmt.Errorf(
				"replace output file: %w",
				renameErr,
			)
		}
	}

	committed = true

	return nil
}
