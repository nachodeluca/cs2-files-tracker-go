package steam

import (
	"github.com/nachodeluca/cs2-files-tracker-go/internal/steam"
)

type Client = steam.Client
type Manifest = steam.Manifest
type DownloadFileResult = steam.DownloadFileResult

func NewClient(appID uint32, depotID uint32, branch string, tempDir string) (*Client, error) {
	return steam.NewClient(appID, depotID, branch, tempDir)
}
