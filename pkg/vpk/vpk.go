package vpk

import internalvpk "github.com/nachodeluca/cs2-files-tracker-go/internal/vpk"

type Reader = internalvpk.Reader

func Open(path string) (*Reader, error) {
	return internalvpk.Open(path)
}

func Files(path string) (files []string, returnErr error) {
	reader, err := internalvpk.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := reader.Close(); err != nil && returnErr == nil {
			returnErr = err
		}
	}()

	return reader.Files(), nil
}

func RequiredArchives(dirPath string, targets []string) ([]uint16, error) {
	return internalvpk.RequiredArchives(dirPath, targets)
}
