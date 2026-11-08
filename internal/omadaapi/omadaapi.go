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

// call performs the request, closes the body, requires a 2xx status, decodes
// the JSON envelope into T and returns an APIError for a non-zero errorCode.
// codeOf and resultOf read the generated envelope fields.
func call[T any](do func() (*http.Response, error), codeOf func(*T) (*int32, *string)) (*T, error) {
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
	var env T
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if code, msg := codeOf(&env); code != nil && *code != 0 {
		m := ""
		if msg != nil {
			m = *msg
		}
		return nil, cloud.NewAPIError(int(*code), m)
	}
	return &env, nil
}

func deref[T any](p *[]T) []T {
	if p == nil {
		return nil
	}
	return *p
}

func total(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// ControllerStatus returns the controller status.
func (c *Client) ControllerStatus(ctx context.Context) (user.ControllerStatus, error) {
	env, err := call(func() (*http.Response, error) {
		return c.cl.Users().GetControllerStatus(ctx, c.omadacID)
	}, func(e *user.OperationResponseControllerStatus) (*int32, *string) { return e.ErrorCode, e.Msg })
	if err != nil {
		return user.ControllerStatus{}, err
	}
	if env.Result == nil {
		return user.ControllerStatus{}, fmt.Errorf("omada: controller status response has no result")
	}
	return *env.Result, nil
}

// Sites returns all sites.
func (c *Client) Sites(ctx context.Context) ([]site.SiteSummaryInfo, error) {
	return cloud.CollectAll(ctx, func(ctx context.Context, page, size int) (cloud.Page[site.SiteSummaryInfo], error) {
		env, err := call(func() (*http.Response, error) {
			return c.cl.Site().GetSiteList(ctx, c.omadacID, &site.GetSiteListParams{Page: int32(page), PageSize: int32(size)})
		}, func(e *site.OperationResponseGridVOSiteSummaryInfo) (*int32, *string) { return e.ErrorCode, e.Msg })
		if err != nil || env.Result == nil {
			return cloud.Page[site.SiteSummaryInfo]{}, err
		}
		return cloud.Page[site.SiteSummaryInfo]{Rows: deref(env.Result.Data), TotalRows: total(env.Result.TotalRows)}, nil
	}, pageSize, maxRows)
}

// Devices returns all devices of a site.
func (c *Client) Devices(ctx context.Context, siteID string) ([]device.DeviceInfo, error) {
	return cloud.CollectAll(ctx, func(ctx context.Context, page, size int) (cloud.Page[device.DeviceInfo], error) {
		env, err := call(func() (*http.Response, error) {
			return c.cl.Devices().GetDeviceList(ctx, c.omadacID, siteID, &device.GetDeviceListParams{Page: int32(page), PageSize: int32(size)})
		}, func(e *device.OperationResponseGridVODeviceInfo) (*int32, *string) { return e.ErrorCode, e.Msg })
		if err != nil || env.Result == nil {
			return cloud.Page[device.DeviceInfo]{}, err
		}
		return cloud.Page[device.DeviceInfo]{Rows: deref(env.Result.Data), TotalRows: total(env.Result.TotalRows)}, nil
	}, pageSize, maxRows)
}

// Ports returns the port counters of all switches of a site.
func (c *Client) Ports(ctx context.Context, siteID string) ([]switchpkg.OswPortsSettingCountersVO, error) {
	return cloud.CollectAll(ctx, func(ctx context.Context, page, size int) (cloud.Page[switchpkg.OswPortsSettingCountersVO], error) {
		env, err := call(func() (*http.Response, error) {
			return c.cl.Switch().GetGridSwitchesPortsCounters(ctx, c.omadacID, siteID, &switchpkg.GetGridSwitchesPortsCountersParams{Page: int32(page), PageSize: int32(size)})
		}, func(e *switchpkg.OperationResponseGridVOOswPortsSettingCountersVO) (*int32, *string) {
			return e.ErrorCode, e.Msg
		})
		if err != nil || env.Result == nil {
			return cloud.Page[switchpkg.OswPortsSettingCountersVO]{}, err
		}
		return cloud.Page[switchpkg.OswPortsSettingCountersVO]{Rows: deref(env.Result.Data), TotalRows: total(env.Result.TotalRows)}, nil
	}, pageSize, maxRows)
}

// Clients returns the active clients of a site, at most 5000.
func (c *Client) Clients(ctx context.Context, siteID string) ([]client.OpenApiClientInfo, error) {
	return cloud.CollectAll(ctx, func(ctx context.Context, page, size int) (cloud.Page[client.OpenApiClientInfo], error) {
		env, err := call(func() (*http.Response, error) {
			return c.cl.Clients().GetGridActiveClients(ctx, c.omadacID, siteID, &client.GetGridActiveClientsParams{Page: int32(page), PageSize: int32(size)})
		}, func(e *client.OperationResponseClientGridVOOpenApiClientInfo) (*int32, *string) {
			return e.ErrorCode, e.Msg
		})
		if err != nil || env.Result == nil {
			return cloud.Page[client.OpenApiClientInfo]{}, err
		}
		return cloud.Page[client.OpenApiClientInfo]{Rows: deref(env.Result.Data), TotalRows: total(env.Result.TotalRows)}, nil
	}, pageSize, maxRows)
}

// Firmware returns the firmware info of one device.
func (c *Client) Firmware(ctx context.Context, siteID, mac string) (device.DeviceFirmwareInfo, error) {
	env, err := call(func() (*http.Response, error) {
		return c.cl.Devices().GetFirmwareInfo(ctx, c.omadacID, siteID, mac)
	}, func(e *device.OperationResponseDeviceFirmwareInfo) (*int32, *string) { return e.ErrorCode, e.Msg })
	if err != nil {
		return device.DeviceFirmwareInfo{}, err
	}
	if env.Result == nil {
		return device.DeviceFirmwareInfo{}, fmt.Errorf("omada: firmware response has no result")
	}
	return *env.Result, nil
}
