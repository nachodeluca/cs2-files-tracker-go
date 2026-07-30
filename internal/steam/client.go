package steam

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	gamejanitor "github.com/warsmite/gamejanitor/steam"
)

type Client struct {
	Client       *gamejanitor.Client
	AppId        uint32
	DepotId      uint32
	Branch       string
	TempDir      string
	ChunkWorkers int

	downloadSessionMu sync.Mutex
	depotKey          []byte
	cdnClient         *gamejanitor.CDNClient
}

func NewClient(appId uint32, depotId uint32, branch string, tempDir string) (*Client, error) {
	if appId == 0 {
		return nil, errors.New("appId cannot be 0")
	}

	if depotId == 0 {
		return nil, errors.New("depotId cannot be 0")
	}

	branch = strings.TrimSpace(branch)
	if branch == "" {
		branch = "public"
	}

	logger := slog.New(
		slog.NewTextHandler(io.Discard, nil),
	)

	steamClient := gamejanitor.NewClient(logger)

	ctx, cancel := context.WithTimeout(
		context.Background(),
		60*time.Second,
	)
	defer cancel()

	if err := connectSteamClient(ctx, steamClient); err != nil {
		_ = steamClient.Close()

		return nil, err
	}

	return &Client{
		Client:       steamClient,
		AppId:        appId,
		DepotId:      depotId,
		Branch:       branch,
		TempDir:      tempDir,
		ChunkWorkers: 16,
	}, nil
}

func connectSteamClient(ctx context.Context, steamClient *gamejanitor.Client) error {
	const attempts = 3

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := steamClient.Connect(ctx); err != nil {
			lastErr = fmt.Errorf("connect to steam (attempt %d/%d): %w", attempt, attempts, err)
		} else if err := steamClient.LoginAnonymous(ctx); err != nil {
			lastErr = fmt.Errorf("login anonymously to steam (attempt %d/%d): %w", attempt, attempts, err)
		} else {
			return nil
		}

		if attempt < attempts {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
	}

	return lastErr
}

func (c *Client) GetManifestId() (string, error) {
	branch := strings.TrimSpace(c.Branch)
	if branch == "" {
		branch = "public"
	}

	ctx, cancel := context.WithTimeout(
		context.Background(),
		30*time.Second,
	)
	defer cancel()

	appInfo, _ := c.Client.GetAppInfo(
		ctx,
		c.AppId,
		branch,
	)

	for _, depot := range appInfo.Depots {
		if depot.DepotID != c.DepotId {
			continue
		}

		if depot.ManifestID == 0 {
			return "", fmt.Errorf(
				"the depot %d has no manifest id for branch %s",
				c.DepotId,
				branch,
			)
		}

		return strconv.FormatUint(depot.ManifestID, 10), nil
	}

	return "", errors.New("no manifest id found for the specified depot and branch")
}

func (c *Client) Close() error {
	if c == nil || c.Client == nil {
		return nil
	}

	return c.Client.Close()
}
