package vpk

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const vpkSignature uint32 = 0x55AA1234
const embeddedArchiveIndex uint16 = 0x7FFF
const entryTerminator uint16 = 0xFFFF

type vpkHeader struct {
	Signature uint32
	Version   uint32
	TreeSize  uint32
}

type vpkHeaderV2 struct {
	FileDataSize   uint32
	ArchiveMD5Size uint32
	OtherMD5Size   uint32
	SignatureSize  uint32
}

type vpkEntry struct {
	CRC          uint32
	PreloadBytes uint16
	ArchiveIndex uint16
	EntryOffset  uint32
	EntryLength  uint32
	Terminator   uint16
}

type fileEntry struct {
	originalPath string
	entry        vpkEntry
	preloadData  []byte
}

type Reader struct {
	file         *os.File
	dirPath      string
	headerSize   uint32
	treeSize     uint32
	fileDataSize uint32
	version      uint32
	entries      map[string]*fileEntry
	mu           sync.Mutex
	openedSlices map[uint16]*os.File
	closed       bool
}

func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open vpk file: %w", err)
	}

	success := false

	defer func() {
		if !success {
			_ = f.Close()
		}
	}()

	var header vpkHeader

	if err := binary.Read(f, binary.LittleEndian, &header); err != nil {
		return nil, fmt.Errorf("read vpk header: %w", err)
	}

	if header.Signature != vpkSignature {
		return nil, fmt.Errorf(
			"invalid vpk signature: expected 0x%08X, got 0x%08X",
			vpkSignature,
			header.Signature,
		)
	}

	var headerSize uint32
	var fileDataSize uint32

	switch header.Version {
	case 1:
		headerSize = 12

	case 2:
		headerSize = 28

		var extra vpkHeaderV2

		if err := binary.Read(f, binary.LittleEndian, &extra); err != nil {
			return nil, fmt.Errorf(
				"read vpk v2 header extension: %w",
				err,
			)
		}

		fileDataSize = extra.FileDataSize

	default:
		return nil, fmt.Errorf(
			"unsupported vpk version: %d",
			header.Version,
		)
	}

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat vpk file: %w", err)
	}

	if info.Size() < 0 {
		return nil, errors.New("invalid negative vpk file size")
	}

	fileSize := uint64(info.Size())
	treeEnd := uint64(headerSize) + uint64(header.TreeSize)

	if treeEnd > fileSize {
		return nil, fmt.Errorf(
			"directory tree exceeds vpk file size: end=%d size=%d",
			treeEnd,
			fileSize,
		)
	}

	if header.Version == 2 {
		dataEnd := treeEnd + uint64(fileDataSize)

		if dataEnd > fileSize {
			return nil, fmt.Errorf(
				"embedded data section exceeds vpk file size: end=%d size=%d",
				dataEnd,
				fileSize,
			)
		}
	}

	if uint64(header.TreeSize) > uint64(maxInt()) {
		return nil, fmt.Errorf(
			"directory tree is too large: %d bytes",
			header.TreeSize,
		)
	}

	if _, err := f.Seek(int64(headerSize), io.SeekStart); err != nil {
		return nil, fmt.Errorf(
			"seek to directory tree start: %w",
			err,
		)
	}

	treeData := make([]byte, int(header.TreeSize))

	if _, err := io.ReadFull(f, treeData); err != nil {
		return nil, fmt.Errorf("read directory tree: %w", err)
	}

	r := &Reader{
		file:         f,
		dirPath:      path,
		headerSize:   headerSize,
		treeSize:     header.TreeSize,
		fileDataSize: fileDataSize,
		version:      header.Version,
		entries:      make(map[string]*fileEntry),
		openedSlices: make(map[uint16]*os.File),
	}

	treeReader := bufio.NewReader(bytes.NewReader(treeData))

	if err := r.parseDirectoryTree(treeReader); err != nil {
		return nil, fmt.Errorf("parse directory tree: %w", err)
	}

	if _, err := treeReader.Peek(1); err == nil {
		return nil, errors.New(
			"directory tree ended before declared tree size",
		)
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf(
			"validate directory tree size: %w",
			err,
		)
	}

	success = true

	return r, nil
}

func readNullTerminatedString(reader *bufio.Reader) (string, error) {
	var buffer []byte

	for {
		b, err := reader.ReadByte()
		if err != nil {
			return "", err
		}

		if b == 0 {
			return string(buffer), nil
		}

		buffer = append(buffer, b)
	}
}

func (r *Reader) parseDirectoryTree(reader *bufio.Reader) error {
	for {
		extension, err := readNullTerminatedString(reader)
		if err != nil {
			return fmt.Errorf("read extension: %w", err)
		}

		if extension == "" {
			return nil
		}

		for {
			directory, err := readNullTerminatedString(reader)
			if err != nil {
				return fmt.Errorf("read directory: %w", err)
			}

			if directory == "" {
				break
			}

			for {
				filename, err := readNullTerminatedString(reader)
				if err != nil {
					return fmt.Errorf("read filename: %w", err)
				}

				if filename == "" {
					break
				}

				var entry vpkEntry

				if err := binary.Read(
					reader,
					binary.LittleEndian,
					&entry,
				); err != nil {
					return fmt.Errorf(
						"read entry for %q: %w",
						filename,
						err,
					)
				}

				if entry.Terminator != entryTerminator {
					return fmt.Errorf(
						"invalid terminator for %q: expected 0x%04X, got 0x%04X",
						filename,
						entryTerminator,
						entry.Terminator,
					)
				}

				preloadData := make(
					[]byte,
					int(entry.PreloadBytes),
				)

				if len(preloadData) > 0 {
					if _, err := io.ReadFull(
						reader,
						preloadData,
					); err != nil {
						return fmt.Errorf(
							"read preload data for %q: %w",
							filename,
							err,
						)
					}
				}

				fullPath := buildEntryPath(
					directory,
					filename,
					extension,
				)

				key := normalizeEntryPath(fullPath)

				if _, exists := r.entries[key]; exists {
					return fmt.Errorf(
						"duplicate vpk entry: %q",
						fullPath,
					)
				}

				r.entries[key] = &fileEntry{
					originalPath: fullPath,
					entry:        entry,
					preloadData:  preloadData,
				}
			}
		}
	}
}

func buildEntryPath(
	directory string,
	filename string,
	extension string,
) string {
	directory = strings.ReplaceAll(directory, "\\", "/")

	fullPath := filename

	if directory != "" && directory != " " {
		fullPath = directory + "/" + filename
	}

	if extension != "" && extension != " " {
		fullPath += "." + extension
	}

	return fullPath
}

func normalizeEntryPath(value string) string {
	value = strings.ReplaceAll(value, "\\", "/")
	value = strings.TrimPrefix(value, "./")

	return strings.ToLower(value)
}

func (r *Reader) getArchiveSlicePath(
	archiveIndex uint16,
) (string, error) {
	dir := filepath.Dir(r.dirPath)
	base := filepath.Base(r.dirPath)

	if !strings.HasSuffix(
		strings.ToLower(base),
		"_dir.vpk",
	) {
		return "", fmt.Errorf(
			"cannot resolve archive %d because %q is not a _dir.vpk file",
			archiveIndex,
			r.dirPath,
		)
	}

	prefix := base[:len(base)-len("_dir.vpk")]

	sliceBase := fmt.Sprintf(
		"%s_%03d.vpk",
		prefix,
		archiveIndex,
	)

	return filepath.Join(dir, sliceBase), nil
}

func (r *Reader) getSliceFileLocked(
	archiveIndex uint16,
) (*os.File, error) {
	if r.closed {
		return nil, errors.New("vpk reader is closed")
	}

	if archiveIndex == embeddedArchiveIndex {
		if r.file == nil {
			return nil, errors.New(
				"vpk directory file is closed",
			)
		}

		return r.file, nil
	}

	if f, ok := r.openedSlices[archiveIndex]; ok {
		return f, nil
	}

	slicePath, err := r.getArchiveSlicePath(archiveIndex)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(slicePath)
	if err != nil {
		return nil, fmt.Errorf(
			"open archive slice %q: %w",
			slicePath,
			err,
		)
	}

	r.openedSlices[archiveIndex] = f

	return f, nil
}

func validateReadRange(
	file *os.File,
	offset uint64,
	length uint64,
) error {
	if offset > math.MaxInt64 {
		return fmt.Errorf(
			"offset exceeds int64: %d",
			offset,
		)
	}

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat archive file: %w", err)
	}

	if info.Size() < 0 {
		return errors.New("invalid negative archive file size")
	}

	fileSize := uint64(info.Size())

	if offset > fileSize {
		return fmt.Errorf(
			"offset %d exceeds file size %d",
			offset,
			fileSize,
		)
	}

	if length > fileSize-offset {
		return fmt.Errorf(
			"range [%d, %d) exceeds file size %d",
			offset,
			offset+length,
			fileSize,
		)
	}

	return nil
}

func (r *Reader) readArchiveAt(
	archiveIndex uint16,
	buffer []byte,
	offset uint64,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	file, err := r.getSliceFileLocked(archiveIndex)
	if err != nil {
		return err
	}

	if err := validateReadRange(
		file,
		offset,
		uint64(len(buffer)),
	); err != nil {
		return err
	}

	n, err := file.ReadAt(buffer, int64(offset))
	if err != nil {
		return err
	}

	if n != len(buffer) {
		return fmt.Errorf(
			"short read: read %d of %d bytes",
			n,
			len(buffer),
		)
	}

	return nil
}

func (r *Reader) ReadFile(filePath string) ([]byte, error) {
	if r == nil {
		return nil, errors.New("vpk reader is nil")
	}

	key := normalizeEntryPath(filePath)

	entry, ok := r.entries[key]
	if !ok {
		return nil, fmt.Errorf(
			"%w: %s",
			os.ErrNotExist,
			filePath,
		)
	}

	preloadLength := uint64(len(entry.preloadData))
	entryLength := uint64(entry.entry.EntryLength)
	totalLength := preloadLength + entryLength

	if totalLength > uint64(maxInt()) {
		return nil, fmt.Errorf(
			"file %q is too large for this platform: %d bytes",
			entry.originalPath,
			totalLength,
		)
	}

	fullData := make([]byte, int(totalLength))
	copy(fullData, entry.preloadData)

	if entryLength > 0 {
		var offset uint64

		if entry.entry.ArchiveIndex == embeddedArchiveIndex {
			relativeOffset := uint64(
				entry.entry.EntryOffset,
			)

			if r.version == 2 {
				if relativeOffset > uint64(r.fileDataSize) {
					return nil, fmt.Errorf(
						"embedded offset for %q exceeds data section: offset=%d size=%d",
						entry.originalPath,
						relativeOffset,
						r.fileDataSize,
					)
				}

				if entryLength >
					uint64(r.fileDataSize)-relativeOffset {
					return nil, fmt.Errorf(
						"embedded data for %q exceeds data section",
						entry.originalPath,
					)
				}
			}

			offset = uint64(r.headerSize) +
				uint64(r.treeSize) +
				relativeOffset
		} else {
			offset = uint64(entry.entry.EntryOffset)
		}

		target := fullData[int(preloadLength):]

		if err := r.readArchiveAt(
			entry.entry.ArchiveIndex,
			target,
			offset,
		); err != nil {
			return nil, fmt.Errorf(
				"read %q from archive %d at offset %d: %w",
				entry.originalPath,
				entry.entry.ArchiveIndex,
				offset,
				err,
			)
		}
	}

	actualCRC := crc32.ChecksumIEEE(fullData)

	if actualCRC != entry.entry.CRC {
		return nil, fmt.Errorf(
			"CRC32 mismatch for %q: expected 0x%08X, got 0x%08X",
			entry.originalPath,
			entry.entry.CRC,
			actualCRC,
		)
	}

	return fullData, nil
}

func (r *Reader) Files() []string {
	if r == nil {
		return nil
	}

	files := make([]string, 0, len(r.entries))

	for _, entry := range r.entries {
		files = append(files, entry.originalPath)
	}

	sort.Strings(files)

	return files
}

func (r *Reader) Close() error {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil
	}

	r.closed = true

	var firstErr error

	for index, file := range r.openedSlices {
		if err := file.Close(); err != nil && firstErr == nil {
			firstErr = err
		}

		delete(r.openedSlices, index)
	}

	if r.file != nil {
		if err := r.file.Close(); err != nil &&
			firstErr == nil {
			firstErr = err
		}

		r.file = nil
	}

	return firstErr
}

func maxInt() int {
	return int(^uint(0) >> 1)
}
