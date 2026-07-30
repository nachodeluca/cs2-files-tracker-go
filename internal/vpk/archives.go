package vpk

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nachodeluca/cs2-files-tracker-go/internal/storage"
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

func Extract(
	dirPath string,
	targets []string,
	outputDir string,
) (returnErr error) {
	dirPath = strings.TrimSpace(dirPath)
	if dirPath == "" {
		return errors.New("vpk directory path cannot be empty")
	}

	outputDir = strings.TrimSpace(outputDir)
	if outputDir == "" {
		return errors.New("output directory cannot be empty")
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf(
			"create output directory %q: %w",
			outputDir,
			err,
		)
	}

	reader, err := Open(dirPath)
	if err != nil {
		return fmt.Errorf("open VPK directory: %w", err)
	}

	defer func() {
		if err := reader.Close(); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("close VPK reader: %w", err)
		}
	}()

	outputNames := make(map[string]string)

	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" {
			return errors.New("target path cannot be empty")
		}

		outputName := filepath.Base(
			filepath.FromSlash(
				strings.ReplaceAll(target, "\\", "/"),
			),
		)

		if outputName == "." || outputName == string(filepath.Separator) {
			return fmt.Errorf("invalid target path %q", target)
		}

		outputKey := strings.ToLower(outputName)

		if previousTarget, exists := outputNames[outputKey]; exists {
			return fmt.Errorf(
				"targets %q and %q produce the same output file %q",
				previousTarget,
				target,
				outputName,
			)
		}

		outputNames[outputKey] = target

		data, err := reader.ReadFile(target)
		if err != nil {
			return fmt.Errorf(
				"read %q from VPK: %w",
				target,
				err,
			)
		}

		data = trimUTF8BOM(data)

		jsonData, err := parseKeyValuesJSON(data)
		if err != nil {
			return fmt.Errorf(
				"parse %q as KeyValues: %w",
				target,
				err,
			)
		}

		jsonOutputName := strings.TrimSuffix(outputName, filepath.Ext(outputName)) + ".json"
		outputPath := filepath.Join(outputDir, jsonOutputName)

		if err := storage.WriteFileAtomic(outputPath, jsonData, 0o644); err != nil {
			return fmt.Errorf(
				"write extracted file %q: %w",
				outputPath,
				err,
			)
		}
	}

	return nil
}

func parseKeyValuesJSON(data []byte) ([]byte, error) {
	parsed, err := parseKeyValues(data)
	if err != nil {
		return nil, err
	}

	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)

	if err := encoder.Encode(parsed); err != nil {
		return nil, err
	}

	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

func RequiredArchives(
	dirPath string,
	targets []string,
) ([]uint16, error) {
	if strings.TrimSpace(dirPath) == "" {
		return nil, errors.New("vpk directory path cannot be empty")
	}

	if len(targets) == 0 {
		return []uint16{}, nil
	}

	reader, err := Open(dirPath)
	if err != nil {
		return nil, fmt.Errorf("open VPK directory: %w", err)
	}
	defer reader.Close()

	archiveSet := make(map[uint16]struct{})
	missingTargets := make([]string, 0)

	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}

		key := normalizeEntryPath(target)

		entry, found := reader.entries[key]
		if !found {
			missingTargets = append(missingTargets, target)
			continue
		}

		if entry.entry.EntryLength == 0 {
			continue
		}

		if entry.entry.ArchiveIndex == embeddedArchiveIndex {
			continue
		}

		archiveSet[entry.entry.ArchiveIndex] = struct{}{}
	}

	if len(missingTargets) > 0 {
		sort.Strings(missingTargets)

		return nil, fmt.Errorf(
			"files not found in VPK: %s",
			strings.Join(missingTargets, ", "),
		)
	}

	archives := make([]uint16, 0, len(archiveSet))

	for archiveIndex := range archiveSet {
		archives = append(archives, archiveIndex)
	}

	sort.Slice(archives, func(i, j int) bool {
		return archives[i] < archives[j]
	})

	return archives, nil
}
