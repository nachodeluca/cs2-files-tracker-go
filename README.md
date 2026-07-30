# CS2 Files Tracker

Downloads CS2 files from Steam and converts the tracked TXT files into JSON.

## Run

```bash
go run ./cmd/extractor/main.go
```

## Output

Files are written to `static/`.

## Reusable packages

Other Go projects can reuse the stable public facades without importing this repository's implementation packages:

- `pkg/steam` connects anonymously, resolves depot manifests, and downloads verified files.
- `pkg/vpk` opens VPK directories and resolves the archive indices required by selected files.
- `pkg/storage` reads manifest state and writes files atomically.

Implementation details remain under `internal/`, so consumers depend only on the small public surface.
