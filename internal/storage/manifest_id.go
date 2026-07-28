package storage

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

func ReadManifestID(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}

	if err != nil {
		return 0, err
	}

	value := strings.TrimSpace(string(data))
	if value == "" {
		return 0, nil
	}

	return strconv.ParseUint(value, 10, 64)
}

func WriteManifestID(path string, id uint64) error {
	return os.WriteFile(path, []byte(strconv.FormatUint(id, 10)), 0o644)
}
