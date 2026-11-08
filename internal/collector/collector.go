// Package collector polls the controller and publishes immutable snapshots.
package collector

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RonaldPhilipsen/omada/cloud/client"
	"github.com/RonaldPhilipsen/omada/cloud/device"
	"github.com/RonaldPhilipsen/omada/cloud/site"
	switchpkg "github.com/RonaldPhilipsen/omada/cloud/switch"
	"github.com/RonaldPhilipsen/omada/cloud/user"

	"github.com/RonaldPhilipsen/omada-exporter/internal/snapshot"
)

// API is the set of controller calls the collector needs.
type API interface {
	ControllerStatus(ctx context.Context) (user.ControllerStatus, error)
	Sites(ctx context.Context) ([]site.SiteSummaryInfo, error)
	Devices(ctx context.Context, siteID string) ([]device.DeviceInfo, error)
	Ports(ctx context.Context, siteID string) ([]switchpkg.OswPortsSettingCountersVO, error)
	Clients(ctx context.Context, siteID string) ([]client.OpenApiClientInfo, error)
	Firmware(ctx context.Context, siteID, mac string) (device.DeviceFirmwareInfo, error)
}

// Recorder receives poll self-metrics.
type Recorder interface {
	PollError(section string)
	PollDuration(d time.Duration)
}

// Options sets the polling cadence.
type Options struct {
	PollInterval     time.Duration
	FirmwareInterval time.Duration
	// FirmwareDelay is the wait between two firmware calls.
	FirmwareDelay time.Duration
}

type siteState struct {
	id, name string
	devices  []snapshot.Device
	ports    []snapshot.Port
	clients  []snapshot.Client
}

// Collector polls the controller and holds the latest snapshot.
type Collector struct {
	api  API
	rec  Recorder
	log  *slog.Logger
	opts Options

	snap atomic.Pointer[snapshot.Snapshot]

	pollMu sync.Mutex // serialises PollOnce calls

	mu         sync.Mutex // guards the fields below
	controller *snapshot.Controller
	order      []string
	sites      map[string]*siteState
	firmware   map[string]*bool // by device MAC
}

// New builds a Collector.
func New(api API, rec Recorder, log *slog.Logger, opts Options) *Collector {
	return &Collector{
		api: api, rec: rec, log: log, opts: opts,
		sites:    map[string]*siteState{},
		firmware: map[string]*bool{},
	}
}

// Snapshot returns the latest snapshot, or nil before the first poll.
// The returned value is never modified.
func (c *Collector) Snapshot() *snapshot.Snapshot { return c.snap.Load() }

func (c *Collector) fail(section string, err error, attrs ...any) {
	c.rec.PollError(section)
	c.log.Error("poll failed", append([]any{"section", section, "error", err}, attrs...)...)
}

// PollOnce polls everything once and publishes a new snapshot.
func (c *Collector) PollOnce(ctx context.Context) {
	c.pollMu.Lock()
	defer c.pollMu.Unlock()
	start := time.Now()
	defer func() { c.rec.PollDuration(time.Since(start)) }()

	var ctrl *snapshot.Controller
	if st, err := c.api.ControllerStatus(ctx); err != nil {
		c.fail("controller", err)
	} else {
		m := mapController(st)
		ctrl = &m
	}

	list, err := c.api.Sites(ctx)
	if err != nil {
		c.fail("sites", err)
		c.mu.Lock()
		if ctrl != nil {
			c.controller = ctrl
		}
		c.mu.Unlock()
		return
	}

	c.mu.Lock()
	prev := c.sites
	c.mu.Unlock()

	next := make(map[string]*siteState, len(list))
	order := make([]string, 0, len(list))
	for _, s := range list {
		id, name := str(s.SiteId), str(s.Name)
		if _, dup := next[id]; dup {
			continue
		}
		st := &siteState{id: id, name: name}
		if p := prev[id]; p != nil {
			*st = *p
			st.name = name
		}
		c.pollSite(ctx, st)
		next[id] = st
		order = append(order, id)
	}

	c.mu.Lock()
	if ctrl != nil {
		c.controller = ctrl
	}
	c.sites, c.order = next, order
	c.publishLocked()
	c.mu.Unlock()
}

// pollSite refreshes each section of st; a failed section keeps its old data.
func (c *Collector) pollSite(ctx context.Context, st *siteState) {
	attrs := []any{"site", st.id}
	if devs, err := c.api.Devices(ctx, st.id); err != nil {
		c.fail("devices", err, attrs...)
	} else {
		st.devices = make([]snapshot.Device, len(devs))
		for i, d := range devs {
			st.devices[i] = mapDevice(d)
		}
	}
	if ports, err := c.api.Ports(ctx, st.id); err != nil {
		c.fail("ports", err, attrs...)
	} else {
		st.ports = make([]snapshot.Port, len(ports))
		for i, p := range ports {
			st.ports[i] = mapPort(p)
		}
	}
	if cls, err := c.api.Clients(ctx, st.id); err != nil {
		c.fail("clients", err, attrs...)
	} else {
		st.clients = make([]snapshot.Client, len(cls))
		for i, cl := range cls {
			st.clients[i] = mapClient(cl)
		}
	}
}

// publishLocked builds a fresh snapshot from the current state. It copies
// every slice, so published snapshots are never mutated. c.mu must be held.
func (c *Collector) publishLocked() {
	out := &snapshot.Snapshot{Sites: make([]snapshot.Site, 0, len(c.order))}
	if c.controller != nil {
		ctrl := *c.controller
		ctrl.Storage = append([]snapshot.Storage(nil), ctrl.Storage...)
		out.Controller = &ctrl
	}
	seen := map[string]bool{}
	for _, id := range c.order {
		st := c.sites[id]
		devs := append([]snapshot.Device(nil), st.devices...)
		for i := range devs {
			seen[devs[i].MAC] = true
			if u, ok := c.firmware[devs[i].MAC]; ok {
				devs[i].UpgradeAvailable = u
			}
		}
		out.Sites = append(out.Sites, snapshot.Site{
			ID: st.id, Name: st.name, Devices: devs,
			Ports:   append([]snapshot.Port(nil), st.ports...),
			Clients: append([]snapshot.Client(nil), st.clients...),
		})
	}
	for mac := range c.firmware {
		if !seen[mac] {
			delete(c.firmware, mac)
		}
	}
	c.snap.Store(out)
}

// PollFirmware fetches firmware info for every device of the current
// snapshot, waiting FirmwareDelay between calls. It does nothing before the
// first successful poll.
func (c *Collector) PollFirmware(ctx context.Context) {
	snap := c.Snapshot()
	if snap == nil {
		return
	}
	type target struct{ site, mac string }
	var targets []target
	for _, s := range snap.Sites {
		for _, d := range s.Devices {
			targets = append(targets, target{s.ID, d.MAC})
		}
	}
	for i, t := range targets {
		if i > 0 && !sleep(ctx, c.opts.FirmwareDelay) {
			return
		}
		if ctx.Err() != nil {
			return
		}
		fw, err := c.api.Firmware(ctx, t.site, t.mac)
		if err != nil {
			c.fail("firmware", err, "site", t.site, "mac", t.mac)
			continue
		}
		u := upgradeAvailable(fw)
		c.mu.Lock()
		c.firmware[t.mac] = u
		c.mu.Unlock()
	}
}

// sleep waits for d or ctx; it reports whether the full wait elapsed.
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// Run polls until ctx is cancelled. It polls once, then runs the main and
// firmware loops; each loop works inline so a poll never overlaps itself.
func (c *Collector) Run(ctx context.Context) error {
	c.PollOnce(ctx)
	var wg sync.WaitGroup
	loop := func(interval time.Duration, first bool, fn func(context.Context)) {
		if interval <= 0 {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if first {
				fn(ctx)
			}
			t := time.NewTicker(interval)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					fn(ctx)
				}
			}
		}()
	}
	loop(c.opts.PollInterval, false, c.PollOnce)
	loop(c.opts.FirmwareInterval, true, c.PollFirmware)
	wg.Wait()
	return nil
}
