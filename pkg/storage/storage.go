package storage

import (
	"os"

	internalstorage "github.com/nachodeluca/cs2-files-tracker-go/internal/storage"
)

func ReadManifestID(path string) (uint64, error) {
	return internalstorage.ReadManifestID(path)
}

func WriteManifestID(path string, id uint64) error {
	return internalstorage.WriteManifestID(path, id)
}

func WriteFileAtomic(path string, data []byte, permissions os.FileMode) error {
	return internalstorage.WriteFileAtomic(path, data, permissions)
}
