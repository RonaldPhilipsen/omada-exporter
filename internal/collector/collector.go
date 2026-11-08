// Package collector polls the controller and publishes immutable snapshots.
package collector

import (
	"context"
	"log/slog"
	"slices"
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
	// CallTimeout bounds each API call. Zero means 30 seconds.
	CallTimeout time.Duration
}

// defaultCallTimeout is the CallTimeout used when Options leaves it zero.
const defaultCallTimeout = 30 * time.Second

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

	ready chan struct{} // closed when the first snapshot is published

	pollMu     sync.Mutex // serialises PollOnce and guards controller and sites
	controller *snapshot.Controller
	sites      []*siteState

	mu       sync.Mutex       // guards firmware
	firmware map[string]*bool // by device MAC
}

// New builds a Collector.
func New(api API, rec Recorder, log *slog.Logger, opts Options) *Collector {
	return &Collector{
		api: api, rec: rec, log: log, opts: opts,
		ready:    make(chan struct{}),
		firmware: map[string]*bool{},
	}
}

// Snapshot returns the latest snapshot, or nil before the first poll.
// The returned value is never modified.
func (c *Collector) Snapshot() *snapshot.Snapshot { return c.snap.Load() }

// timed runs fn with a context that ends after the call timeout.
func timed[T any](c *Collector, ctx context.Context, fn func(context.Context) (T, error)) (T, error) {
	d := c.opts.CallTimeout
	if d <= 0 {
		d = defaultCallTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	return fn(ctx)
}

// fail records a failed section. It ignores errors caused by shutdown.
func (c *Collector) fail(ctx context.Context, section string, err error, attrs ...any) {
	if ctx.Err() != nil {
		return
	}
	c.rec.PollError(section)
	c.log.Error("poll failed", append([]any{"section", section, "error", err}, attrs...)...)
}

// PollOnce polls everything once and publishes a new snapshot.
func (c *Collector) PollOnce(ctx context.Context) {
	c.pollMu.Lock()
	defer c.pollMu.Unlock()
	start := time.Now()
	defer func() { c.rec.PollDuration(time.Since(start)) }()

	if st, err := timed(c, ctx, c.api.ControllerStatus); err != nil {
		c.fail(ctx, "controller", err)
	} else {
		m := mapController(st)
		c.controller = &m
	}

	list, err := timed(c, ctx, c.api.Sites)
	if err != nil {
		c.fail(ctx, "sites", err)
		return
	}

	prev := make(map[string]*siteState, len(c.sites))
	for _, st := range c.sites {
		prev[st.id] = st
	}
	seen := make(map[string]bool, len(list))
	next := make([]*siteState, 0, len(list))
	for _, s := range list {
		id := str(s.SiteId)
		if seen[id] {
			continue
		}
		seen[id] = true
		st := prev[id]
		if st == nil {
			st = &siteState{id: id}
		}
		st.name = str(s.Name)
		c.pollSite(ctx, st)
		next = append(next, st)
	}
	c.sites = next
	c.publish()
}

// pollSite refreshes each section of st; a failed section keeps its old data.
func (c *Collector) pollSite(ctx context.Context, st *siteState) {
	refresh(c, ctx, "devices", st.id, c.api.Devices, mapAll(mapDevice), &st.devices)
	refresh(c, ctx, "ports", st.id, c.api.Ports, mapPorts, &st.ports)
	refresh(c, ctx, "clients", st.id, c.api.Clients, mapAll(mapClient), &st.clients)
}

// refresh fetches one section of a site and replaces *dst on success.
func refresh[R, S any](c *Collector, ctx context.Context, section, siteID string,
	fetch func(context.Context, string) ([]R, error), convert func([]R) []S, dst *[]S) {
	rows, err := timed(c, ctx, func(ctx context.Context) ([]R, error) { return fetch(ctx, siteID) })
	if err != nil {
		c.fail(ctx, section, err, "site", siteID)
		return
	}
	*dst = convert(rows)
}

// mapAll lifts a row mapper to a slice mapper.
func mapAll[R, S any](f func(R) S) func([]R) []S {
	return func(rows []R) []S {
		out := make([]S, len(rows))
		for i, r := range rows {
			out[i] = f(r)
		}
		return out
	}
}

// mapPorts maps port rows and drops rows without a port number.
func mapPorts(rows []switchpkg.OswPortsSettingCountersVO) []snapshot.Port {
	out := make([]snapshot.Port, 0, len(rows))
	for _, p := range rows {
		if p.Port != nil {
			out = append(out, mapPort(p))
		}
	}
	return out
}

// publish builds and stores a snapshot from the current state. c.pollMu must
// be held. Section slices are replaced on each poll and never modified, so the
// snapshot shares them. Only devices are copied, to merge firmware results.
func (c *Collector) publish() {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := &snapshot.Snapshot{Controller: c.controller, Sites: make([]snapshot.Site, 0, len(c.sites))}
	seen := map[string]bool{}
	for _, st := range c.sites {
		devs := slices.Clone(st.devices)
		for i := range devs {
			seen[devs[i].MAC] = true
			if u, ok := c.firmware[devs[i].MAC]; ok {
				devs[i].UpgradeAvailable = u
			}
		}
		out.Sites = append(out.Sites, snapshot.Site{
			ID: st.id, Name: st.name, Devices: devs, Ports: st.ports, Clients: st.clients,
		})
	}
	for mac := range c.firmware {
		if !seen[mac] {
			delete(c.firmware, mac)
		}
	}
	if c.snap.Swap(out) == nil {
		close(c.ready)
	}
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
		fw, err := timed(c, ctx, func(ctx context.Context) (device.DeviceFirmwareInfo, error) {
			return c.api.Firmware(ctx, t.site, t.mac)
		})
		if err != nil {
			c.fail(ctx, "firmware", err, "site", t.site, "mac", t.mac)
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

// Run polls until ctx is cancelled. Each loop works inline, so a poll never
// overlaps itself. The first firmware pass starts after the first snapshot.
func (c *Collector) Run(ctx context.Context) error {
	c.PollOnce(ctx)
	var wg sync.WaitGroup
	loop := func(interval time.Duration, first func(context.Context), fn func(context.Context)) {
		if interval <= 0 {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			first(ctx)
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
	loop(c.opts.PollInterval, func(context.Context) {}, c.PollOnce)
	loop(c.opts.FirmwareInterval, func(ctx context.Context) {
		select {
		case <-c.ready:
			c.PollFirmware(ctx)
		case <-ctx.Done():
		}
	}, c.PollFirmware)
	wg.Wait()
	return nil
}
