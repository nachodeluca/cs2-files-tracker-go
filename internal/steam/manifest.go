package steam

import (
	"context"
	"errors"
	"fmt"

	gamejanitor "github.com/warsmite/gamejanitor/steam"
)

func (c *Client) GetManifest(ctx context.Context, manifestID uint64) (*Manifest, error) {
	if c == nil || c.Client == nil {
		return nil, errors.New("steam client is not initialized")
	}

	if manifestID == 0 {
		return nil, errors.New("manifest ID cannot be zero")
	}

	branch := c.Branch
	if branch == "" {
		branch = "public"
	}

	depotKey, err := c.Client.GetDepotDecryptionKey(
		ctx,
		c.DepotId,
		c.AppId,
	)

	if err != nil {
		return nil, fmt.Errorf(
			"get decryption key for depot %d: %w",
			c.DepotId,
			err,
		)
	}

	if len(depotKey) == 0 {
		return nil, fmt.Errorf(
			"Steam returned an empty key for depot %d",
			c.DepotId,
		)
	}

	requestCode, err := c.Client.GetManifestRequestCode(
		ctx,
		c.AppId,
		c.DepotId,
		manifestID,
		branch,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"get request code for manifest %d: %w",
			manifestID,
			err,
		)
	}

	cdnHosts, err := c.Client.GetCDNServers(ctx, 0)
	if err != nil {
		return nil, fmt.Errorf(
			"get Steam CDN servers: %w",
			err,
		)
	}

	if len(cdnHosts) == 0 {
		return nil, errors.New("Steam returned no CDN servers")
	}

	manifest, err := gamejanitor.DownloadManifest(
		ctx,
		cdnHosts,
		c.DepotId,
		manifestID,
		requestCode,
		depotKey,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"download manifest %d for depot %d: %w",
			manifestID,
			c.DepotId,
			err,
		)
	}

	return manifest, nil
}
