package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/nachodeluca/cs2-files-tracker-go/internal/config"
	"github.com/nachodeluca/cs2-files-tracker-go/internal/steam"
	"github.com/nachodeluca/cs2-files-tracker-go/internal/storage"
	"github.com/nachodeluca/cs2-files-tracker-go/internal/vpk"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSliceFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("target cannot be empty")
	}

	*s = append(*s, value)
	return nil
}

func run(args []string) error {
	cfg := config.Default()
	var appID uint64 = uint64(cfg.AppId)
	var depotID uint64 = uint64(cfg.DepotID)

	var targets stringSliceFlag

	fs := flag.NewFlagSet("extractor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Uint64Var(&appID, "app-id", appID, "Steam app ID")
	fs.Uint64Var(&depotID, "depot-id", depotID, "Steam depot ID")
	fs.StringVar(&cfg.Branch, "branch", cfg.Branch, "Steam branch")
	fs.StringVar(&cfg.TempDir, "temp-dir", cfg.TempDir, "temporary download directory")
	fs.StringVar(&cfg.OutputDir, "output-dir", cfg.OutputDir, "output directory")
	fs.StringVar(&cfg.ManifestIDPath, "manifest-id-path", cfg.ManifestIDPath, "manifest ID cache path")
	fs.Var(&targets, "target", "file to extract from the VPK (repeatable)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	log.Printf("starting extractor: app-id=%d depot-id=%d branch=%s", appID, depotID, cfg.Branch)

	if appID == 0 || appID > uint64(^uint32(0)) {
		return fmt.Errorf("invalid app-id %d", appID)
	}

	if depotID == 0 || depotID > uint64(^uint32(0)) {
		return fmt.Errorf("invalid depot-id %d", depotID)
	}

	cfg.AppId = uint32(appID)
	cfg.DepotID = uint32(depotID)

	if strings.TrimSpace(cfg.ManifestIDPath) == "" {
		cfg.ManifestIDPath = filepath.Join(cfg.OutputDir, "manifest_id.txt")
	}

	if len(targets) == 0 {
		targets = append(targets, cfg.TargetFiles...)
	}

	if len(targets) == 0 {
		return errors.New("no target files configured")
	}

	if err := os.MkdirAll(cfg.TempDir, 0o755); err != nil {
		return fmt.Errorf("create temp directory %q: %w", cfg.TempDir, err)
	}

	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return fmt.Errorf("create output directory %q: %w", cfg.OutputDir, err)
	}

	log.Printf("connecting to Steam")
	client, err := steam.NewClient(cfg.AppId, cfg.DepotID, cfg.Branch, cfg.TempDir)
	if err != nil {
		return err
	}
	defer client.Close()

	ctx := context.Background()

	log.Printf("reading cached manifest id from %s", cfg.ManifestIDPath)
	manifestID, err := storage.ReadManifestID(cfg.ManifestIDPath)
	if err != nil {
		return fmt.Errorf("read manifest id: %w", err)
	}

	log.Printf("resolving current manifest id")
	currentManifestID, err := client.GetManifestID(ctx)
	if err != nil {
		return err
	}

	needsUpdate := manifestID == 0 || manifestID != currentManifestID

	if !needsUpdate {
		log.Printf("manifest %d is already processed", currentManifestID)

		return nil
	}

	manifestID = currentManifestID

	log.Printf("downloading manifest %d", manifestID)
	manifest, err := client.GetManifest(ctx, manifestID)
	if err != nil {
		return err
	}

	log.Printf("downloading VPK directory file")
	dirPath, err := downloadVPKFile(ctx, client, manifest, "game/csgo/pak01_dir.vpk")
	if err != nil {
		return err
	}

	log.Printf("resolving required archives")
	archives, err := vpk.RequiredArchives(dirPath, targets)
	if err != nil {
		return err
	}

	for index, archiveIndex := range archives {
		archiveName := fmt.Sprintf("game/csgo/pak01_%03d.vpk", archiveIndex)
		log.Printf(
			"downloading archive %d/%d: %s",
			index+1,
			len(archives),
			archiveName,
		)
		if _, err := downloadVPKFile(ctx, client, manifest, archiveName); err != nil {
			return err
		}
	}

	log.Printf("extracting %d target file(s) to %s", len(targets), cfg.OutputDir)
	if err := vpk.Extract(dirPath, targets, cfg.OutputDir); err != nil {
		return err
	}

	if err := storage.WriteManifestID(cfg.ManifestIDPath, manifestID); err != nil {
		return fmt.Errorf("write manifest id: %w", err)
	}

	log.Printf("manifest %d successfully processed", manifestID)

	return nil
}

func downloadVPKFile(
	ctx context.Context,
	client *steam.Client,
	manifest *steam.Manifest,
	name string,
) (string, error) {
	log.Printf("downloading %s", name)
	path, err := client.DownloadFile(ctx, manifest, name)
	if err != nil {
		return "", err
	}

	return path, nil
}
