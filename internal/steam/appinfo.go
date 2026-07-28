package steam

import (
	"context"
	"errors"
	"fmt"
)

func (c *Client) GetManifestID(ctx context.Context) (uint64, error) {
	if c == nil || c.Client == nil {
		return 0, errors.New("steam client is not initialized")
	}

	branch := c.Branch
	if branch == "" {
		branch = "public"
	}

	appInfo, err := c.Client.GetAppInfo(ctx, c.AppId, branch)
	if err != nil {
		return 0, fmt.Errorf(
			"get app information %d: %w",
			c.AppId,
			err,
		)
	}

	for _, depot := range appInfo.Depots {
		if depot.DepotID != c.DepotId {
			continue
		}

		if depot.ManifestID == 0 {
			return 0, fmt.Errorf(
				"the depot %d has no manifest id for branch %s",
				c.DepotId,
				branch,
			)
		}

		return depot.ManifestID, nil
	}

	return 0, fmt.Errorf(
		"no manifest id found for the specified depot %d and app %d",
		c.DepotId,
		c.AppId,
	)
}
