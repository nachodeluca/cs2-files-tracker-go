package steam

import (
	"context"
	"crypto/sha1"
	"os"
	"path/filepath"
	"testing"

	gamejanitor "github.com/warsmite/gamejanitor/steam"
)

func TestDownloadFileWithStatusReusesMatchingFile(t *testing.T) {
	root := t.TempDir()
	name := "game/csgo/pak01_001.vpk"
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	content := []byte("existing VPK content")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha1.Sum(content)

	client := &Client{
		Client:  &gamejanitor.Client{},
		AppId:   730,
		DepotId: 2347770,
		TempDir: root,
	}
	manifest := &Manifest{
		DepotID: 2347770,
		Files: []gamejanitor.ManifestFile{{
			Filename:   name,
			Size:       uint64(len(content)),
			SHAContent: sum[:],
		}},
	}

	result, err := client.DownloadFileWithStatus(
		context.Background(),
		manifest,
		name,
	)
	if err != nil {
		t.Fatalf("DownloadFileWithStatus() error = %v", err)
	}
	if result.Downloaded {
		t.Fatal("Downloaded = true, want matching file to be reused")
	}
	if result.Path != path {
		t.Fatalf("Path = %q, want %q", result.Path, path)
	}
	if result.Size != uint64(len(content)) {
		t.Fatalf("Size = %d, want %d", result.Size, len(content))
	}
}

func TestExistingFileMatchesRejectsWrongContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.vpk")
	content := []byte("wrong content")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	expected := sha1.Sum([]byte("right content"))
	matches, err := existingFileMatches(
		context.Background(),
		path,
		uint64(len(content)),
		expected[:],
	)
	if err != nil {
		t.Fatalf("existingFileMatches() error = %v", err)
	}
	if matches {
		t.Fatal("existingFileMatches() = true, want false")
	}
}

func TestExistingFileMatchesHonorsCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.vpk")
	content := make([]byte, 2*1024*1024)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha1.Sum(content)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := existingFileMatches(
		ctx,
		path,
		uint64(len(content)),
		sum[:],
	); err == nil {
		t.Fatal("existingFileMatches() error = nil, want cancellation")
	}
}

func TestChunkWorkersUsesSafeBounds(t *testing.T) {
	client := &Client{}

	if workers := client.chunkWorkers(); workers != 16 {
		t.Fatalf("default chunk workers = %d, want 16", workers)
	}

	client.ChunkWorkers = 32
	if workers := client.chunkWorkers(); workers != 32 {
		t.Fatalf("configured chunk workers = %d, want 32", workers)
	}

	client.ChunkWorkers = 100
	if workers := client.chunkWorkers(); workers != 64 {
		t.Fatalf("capped chunk workers = %d, want 64", workers)
	}
}
