// Package omadaapi adapts the generated Omada northbound client to the
// small set of calls the exporter needs. Paged lists are collected in full
// (up to a row cap) and envelope errors become *cloud.APIError.
package omadaapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/RonaldPhilipsen/omada/cloud"
	"github.com/RonaldPhilipsen/omada/cloud/client"
	"github.com/RonaldPhilipsen/omada/cloud/device"
	"github.com/RonaldPhilipsen/omada/cloud/site"
	switchpkg "github.com/RonaldPhilipsen/omada/cloud/switch"
	"github.com/RonaldPhilipsen/omada/cloud/user"

	"github.com/RonaldPhilipsen/omada-exporter/internal/config"
)

const (
	pageSize = 100
	maxRows  = 5000
	// maxBody caps how much of a response we read.
	maxBody = 32 << 20
)

// Client wraps the generated Omada client for one controller.
type Client struct {
	cl       *cloud.Client
	omadacID string
}

// New builds a Client for the on-prem controller described by cfg.
func New(cfg config.Config) (*Client, error) {
	cl, err := cloud.New(cloud.Options{
		BaseURL:      cfg.BaseURL,
		OmadacID:     cfg.OmadacID,
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		InsecureTLS:  cfg.InsecureTLS,
	})
	if err != nil {
		return nil, fmt.Errorf("create omada client: %w", err)
	}
	return &Client{cl: cl, omadacID: cfg.OmadacID}, nil
}

// envelope is the wrapper around every Omada response.
type envelope[T any] struct {
	ErrorCode *int32  `json:"errorCode"`
	Msg       *string `json:"msg"`
	Result    *T      `json:"result"`
}

// grid is the result of a paged operation.
type grid[Row any] struct {
	TotalRows int64 `json:"totalRows"`
	Data      []Row `json:"data"`
}

// call performs the request, closes the body, requires a 2xx status and
// decodes the envelope. A missing errorCode or result is an error, and a
// non-zero errorCode returns an *cloud.APIError.
func call[T any](do func() (*http.Response, error)) (*T, error) {
	resp, err := do()
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("omada: unexpected HTTP status %d", resp.StatusCode)
	}
	var env envelope[T]
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if env.ErrorCode == nil {
		return nil, fmt.Errorf("omada: response has no errorCode")
	}
	if *env.ErrorCode != 0 {
		msg := ""
		if env.Msg != nil {
			msg = *env.Msg
		}
		return nil, cloud.NewAPIError(int(*env.ErrorCode), msg)
	}
	if env.Result == nil {
		return nil, fmt.Errorf("omada: response has no result")
	}
	return env.Result, nil
}

// list collects every page of a paged operation, up to maxRows.
func list[Row any](ctx context.Context, fetch func(ctx context.Context, page, size int32) (*http.Response, error)) ([]Row, error) {
	return cloud.CollectAll(ctx, func(ctx context.Context, page, size int) (cloud.Page[Row], error) {
		g, err := call[grid[Row]](func() (*http.Response, error) {
			return fetch(ctx, int32(page), int32(size))
		})
		if err != nil {
			return cloud.Page[Row]{}, err
		}
		return cloud.Page[Row]{Rows: g.Data, TotalRows: g.TotalRows}, nil
	}, pageSize, maxRows)
}

// ControllerStatus returns the controller status.
func (c *Client) ControllerStatus(ctx context.Context) (user.ControllerStatus, error) {
	st, err := call[user.ControllerStatus](func() (*http.Response, error) {
		return c.cl.Users().GetControllerStatus(ctx, c.omadacID)
	})
	if err != nil {
		return user.ControllerStatus{}, err
	}
	return *st, nil
}

// Sites returns all sites.
func (c *Client) Sites(ctx context.Context) ([]site.SiteSummaryInfo, error) {
	return list[site.SiteSummaryInfo](ctx, func(ctx context.Context, page, size int32) (*http.Response, error) {
		return c.cl.Site().GetSiteList(ctx, c.omadacID, &site.GetSiteListParams{Page: page, PageSize: size})
	})
}

// Devices returns all devices of a site.
func (c *Client) Devices(ctx context.Context, siteID string) ([]device.DeviceInfo, error) {
	return list[device.DeviceInfo](ctx, func(ctx context.Context, page, size int32) (*http.Response, error) {
		return c.cl.Devices().GetDeviceList(ctx, c.omadacID, siteID, &device.GetDeviceListParams{Page: page, PageSize: size})
	})
}

// Ports returns the port counters of all switches of a site.
func (c *Client) Ports(ctx context.Context, siteID string) ([]switchpkg.OswPortsSettingCountersVO, error) {
	return list[switchpkg.OswPortsSettingCountersVO](ctx, func(ctx context.Context, page, size int32) (*http.Response, error) {
		return c.cl.Switch().GetGridSwitchesPortsCounters(ctx, c.omadacID, siteID, &switchpkg.GetGridSwitchesPortsCountersParams{Page: page, PageSize: size})
	})
}

// Clients returns the active clients of a site, at most 5000.
func (c *Client) Clients(ctx context.Context, siteID string) ([]client.OpenApiClientInfo, error) {
	return list[client.OpenApiClientInfo](ctx, func(ctx context.Context, page, size int32) (*http.Response, error) {
		return c.cl.Clients().GetGridActiveClients(ctx, c.omadacID, siteID, &client.GetGridActiveClientsParams{Page: page, PageSize: size})
	})
}

// Firmware returns the firmware info of one device.
func (c *Client) Firmware(ctx context.Context, siteID, mac string) (device.DeviceFirmwareInfo, error) {
	fw, err := call[device.DeviceFirmwareInfo](func() (*http.Response, error) {
		return c.cl.Devices().GetFirmwareInfo(ctx, c.omadacID, siteID, mac)
	})
	if err != nil {
		return device.DeviceFirmwareInfo{}, err
	}
	return *fw, nil
}
