package steam

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLiveExistingVPKsMatchCurrentManifest(t *testing.T) {
	root := os.Getenv("CS2_LIVE_VPK_DIR")
	if root == "" {
		t.Skip("set CS2_LIVE_VPK_DIR to run the live Steam verification")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client, err := NewClient(730, 2347770, "public", root)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()

	manifestID, err := client.GetManifestID(ctx)
	if err != nil {
		t.Fatalf("GetManifestID() error = %v", err)
	}
	manifest, err := client.GetManifest(ctx, manifestID)
	if err != nil {
		t.Fatalf("GetManifest() error = %v", err)
	}

	vpkRoot := filepath.Join(root, "game", "csgo")
	entries, err := os.ReadDir(vpkRoot)
	if err != nil {
		t.Fatalf("ReadDir(%q) error = %v", vpkRoot, err)
	}

	verified := 0
	for _, entry := range entries {
		name := strings.ToLower(entry.Name())
		if entry.IsDir() ||
			!strings.HasPrefix(name, "pak01_") ||
			!strings.HasSuffix(name, ".vpk") {
			continue
		}

		manifestName := "game/csgo/" + entry.Name()
		manifestFile, err := findManifestFile(manifest, manifestName)
		if err != nil {
			t.Errorf("%s: %v", entry.Name(), err)
			continue
		}

		matches, err := existingFileMatches(
			ctx,
			filepath.Join(vpkRoot, entry.Name()),
			manifestFile.Size,
			manifestFile.SHAContent,
		)
		if err != nil {
			t.Errorf("%s: verify: %v", entry.Name(), err)
			continue
		}
		if !matches {
			t.Errorf("%s does not match manifest %d", entry.Name(), manifestID)
			continue
		}
		verified++
	}

	if verified == 0 {
		t.Fatal("no pak01 VPK files were verified")
	}
	t.Logf("verified %d existing VPK files against manifest %d", verified, manifestID)
}

func TestLiveDownloadSingleVPK(t *testing.T) {
	root := os.Getenv("CS2_LIVE_VPK_DIR")
	target := os.Getenv("CS2_LIVE_DOWNLOAD_FILE")
	if root == "" || target == "" {
		t.Skip(
			"set CS2_LIVE_VPK_DIR and CS2_LIVE_DOWNLOAD_FILE " +
				"to run a live download benchmark",
		)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	client, err := NewClient(730, 2347770, "public", root)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()
	client.ChunkWorkers = 16

	manifestID, err := client.GetManifestID(ctx)
	if err != nil {
		t.Fatalf("GetManifestID() error = %v", err)
	}
	manifest, err := client.GetManifest(ctx, manifestID)
	if err != nil {
		t.Fatalf("GetManifest() error = %v", err)
	}

	started := time.Now()
	result, err := client.DownloadFileWithStatus(ctx, manifest, target)
	if err != nil {
		t.Fatalf("DownloadFileWithStatus() error = %v", err)
	}
	elapsed := time.Since(started)
	megabytes := float64(result.Size) / (1024 * 1024)
	megabytesPerSecond := megabytes / elapsed.Seconds()

	t.Logf(
		"downloaded=%t size=%.1f MiB elapsed=%s throughput=%.1f MiB/s",
		result.Downloaded,
		megabytes,
		elapsed.Round(time.Millisecond),
		megabytesPerSecond,
	)
}

func TestLiveDownloadVPKsParallel(t *testing.T) {
	root := os.Getenv("CS2_LIVE_VPK_DIR")
	targetValue := os.Getenv("CS2_LIVE_DOWNLOAD_FILES")
	if root == "" || targetValue == "" {
		t.Skip(
			"set CS2_LIVE_VPK_DIR and CS2_LIVE_DOWNLOAD_FILES " +
				"to run a parallel live download benchmark",
		)
	}
	targets := strings.Split(targetValue, ",")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	client, err := NewClient(730, 2347770, "public", root)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()
	client.ChunkWorkers = 16

	manifestID, err := client.GetManifestID(ctx)
	if err != nil {
		t.Fatalf("GetManifestID() error = %v", err)
	}
	manifest, err := client.GetManifest(ctx, manifestID)
	if err != nil {
		t.Fatalf("GetManifest() error = %v", err)
	}

	started := time.Now()
	var workers sync.WaitGroup
	var resultMu sync.Mutex
	var totalBytes uint64
	var firstError error

	for _, target := range targets {
		target = strings.TrimSpace(target)
		workers.Add(1)
		go func() {
			defer workers.Done()

			result, err := client.DownloadFileWithStatus(
				ctx,
				manifest,
				target,
			)
			resultMu.Lock()
			defer resultMu.Unlock()
			if err != nil && firstError == nil {
				firstError = err
				cancel()
				return
			}
			totalBytes += result.Size
		}()
	}
	workers.Wait()
	if firstError != nil {
		t.Fatalf("parallel download error = %v", firstError)
	}

	elapsed := time.Since(started)
	megabytes := float64(totalBytes) / (1024 * 1024)
	megabytesPerSecond := megabytes / elapsed.Seconds()
	t.Logf(
		"files=%d size=%.1f MiB elapsed=%s throughput=%.1f MiB/s",
		len(targets),
		megabytes,
		elapsed.Round(time.Millisecond),
		megabytesPerSecond,
	)
}
