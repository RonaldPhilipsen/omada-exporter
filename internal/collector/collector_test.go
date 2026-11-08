package collector

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RonaldPhilipsen/omada/cloud/client"
	"github.com/RonaldPhilipsen/omada/cloud/device"
	"github.com/RonaldPhilipsen/omada/cloud/site"
	switchpkg "github.com/RonaldPhilipsen/omada/cloud/switch"
	"github.com/RonaldPhilipsen/omada/cloud/user"
)

func sp(s string) *string { return &s }

type fakeAPI struct {
	mu       sync.Mutex
	sites    []site.SiteSummaryInfo
	devices  map[string][]device.DeviceInfo
	ports    map[string][]switchpkg.OswPortsSettingCountersVO
	clients  map[string][]client.OpenApiClientInfo
	fw       map[string]device.DeviceFirmwareInfo
	fail     map[string]bool
	fwCalls  int
	fwCalled chan struct{}
	sitesFn  func(ctx context.Context) ([]site.SiteSummaryInfo, error)
}

func newFake() *fakeAPI {
	return &fakeAPI{
		devices: map[string][]device.DeviceInfo{},
		ports:   map[string][]switchpkg.OswPortsSettingCountersVO{},
		clients: map[string][]client.OpenApiClientInfo{},
		fw:      map[string]device.DeviceFirmwareInfo{},
		fail:    map[string]bool{},
	}
}

func (f *fakeAPI) set(fn func()) { f.mu.Lock(); defer f.mu.Unlock(); fn() }

func (f *fakeAPI) err(section string) error {
	if f.fail[section] {
		return errors.New(section + " failed")
	}
	return nil
}

func (f *fakeAPI) ControllerStatus(ctx context.Context) (user.ControllerStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return user.ControllerStatus{}, f.err("controller")
}

func (f *fakeAPI) Sites(ctx context.Context) ([]site.SiteSummaryInfo, error) {
	if f.sitesFn != nil {
		return f.sitesFn(ctx)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("sites"); err != nil {
		return nil, err
	}
	return append([]site.SiteSummaryInfo(nil), f.sites...), nil
}

func (f *fakeAPI) Devices(ctx context.Context, id string) ([]device.DeviceInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("devices"); err != nil {
		return nil, err
	}
	return append([]device.DeviceInfo(nil), f.devices[id]...), nil
}

func (f *fakeAPI) Ports(ctx context.Context, id string) ([]switchpkg.OswPortsSettingCountersVO, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("ports"); err != nil {
		return nil, err
	}
	return append([]switchpkg.OswPortsSettingCountersVO(nil), f.ports[id]...), nil
}

func (f *fakeAPI) Clients(ctx context.Context, id string) ([]client.OpenApiClientInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("clients"); err != nil {
		return nil, err
	}
	return append([]client.OpenApiClientInfo(nil), f.clients[id]...), nil
}

func (f *fakeAPI) Firmware(ctx context.Context, siteID, mac string) (device.DeviceFirmwareInfo, error) {
	f.mu.Lock()
	f.fwCalls++
	ch := f.fwCalled
	err := f.err("firmware")
	fw := f.fw[mac]
	f.mu.Unlock()
	if ch != nil {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	return fw, err
}

type fakeRec struct {
	mu   sync.Mutex
	errs map[string]int
	durs int
}

func newRec() *fakeRec { return &fakeRec{errs: map[string]int{}} }

func (r *fakeRec) PollError(s string)         { r.mu.Lock(); r.errs[s]++; r.mu.Unlock() }
func (r *fakeRec) PollDuration(time.Duration) { r.mu.Lock(); r.durs++; r.mu.Unlock() }
func (r *fakeRec) count(s string) int         { r.mu.Lock(); defer r.mu.Unlock(); return r.errs[s] }
func (r *fakeRec) total() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, v := range r.errs {
		n += v
	}
	return n
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func oneSite() *fakeAPI {
	f := newFake()
	f.sites = []site.SiteSummaryInfo{{SiteId: sp("s1"), Name: sp("Home")}}
	f.devices["s1"] = []device.DeviceInfo{{Mac: sp("aa"), Name: sp("sw1")}}
	f.ports["s1"] = []switchpkg.OswPortsSettingCountersVO{{SwitchMac: sp("aa"), Port: ptr32(1)}}
	f.clients["s1"] = []client.OpenApiClientInfo{{Mac: sp("c1")}}
	return f
}

func ptr32(v int32) *int32 { return &v }

func newC(f *fakeAPI, r *fakeRec, o Options) *Collector { return New(f, r, quiet(), o) }

func TestPollPublishesSnapshot(t *testing.T) {
	c := newC(oneSite(), newRec(), Options{})
	if c.Snapshot() != nil {
		t.Fatal("expected nil before first poll")
	}
	c.PollOnce(context.Background())
	s := c.Snapshot()
	if s == nil || len(s.Sites) != 1 {
		t.Fatalf("snapshot = %+v", s)
	}
	st := s.Sites[0]
	if st.ID != "s1" || st.Name != "Home" || len(st.Devices) != 1 || len(st.Ports) != 1 || len(st.Clients) != 1 {
		t.Fatalf("site = %+v", st)
	}
	if s.Controller == nil {
		t.Fatal("controller missing")
	}
}

func TestSectionFailureKeepsLastGood(t *testing.T) {
	f, r := oneSite(), newRec()
	c := newC(f, r, Options{})
	c.PollOnce(context.Background())
	f.set(func() {
		f.fail["ports"] = true
		f.devices["s1"] = []device.DeviceInfo{{Mac: sp("aa"), Name: sp("sw1")}, {Mac: sp("bb"), Name: sp("sw2")}}
		f.ports["s1"] = nil
	})
	c.PollOnce(context.Background())
	st := c.Snapshot().Sites[0]
	if len(st.Ports) != 1 || len(st.Devices) != 2 {
		t.Fatalf("ports=%d devices=%d", len(st.Ports), len(st.Devices))
	}
	if r.count("ports") != 1 || r.total() != 1 {
		t.Fatalf("errs = %v", r.errs)
	}
}

func TestSiteListFailureKeepsSnapshot(t *testing.T) {
	f, r := oneSite(), newRec()
	c := newC(f, r, Options{})
	c.PollOnce(context.Background())
	before := c.Snapshot()
	f.set(func() { f.fail["sites"] = true; f.devices["s1"] = nil })
	c.PollOnce(context.Background())
	after := c.Snapshot()
	if len(after.Sites) != 1 || len(after.Sites[0].Devices) != 1 || len(before.Sites[0].Devices) != 1 {
		t.Fatalf("contents changed: %+v", after)
	}
	if r.count("sites") != 1 {
		t.Fatalf("errs = %v", r.errs)
	}
}

func TestSiteRemoved(t *testing.T) {
	f := oneSite()
	f.sites = append(f.sites, site.SiteSummaryInfo{SiteId: sp("s2"), Name: sp("Other")})
	c := newC(f, newRec(), Options{})
	c.PollOnce(context.Background())
	if n := len(c.Snapshot().Sites); n != 2 {
		t.Fatalf("sites = %d", n)
	}
	f.set(func() { f.sites = f.sites[:1] })
	c.PollOnce(context.Background())
	s := c.Snapshot()
	if len(s.Sites) != 1 || s.Sites[0].ID != "s1" {
		t.Fatalf("sites = %+v", s.Sites)
	}
}

func TestDepartedClient(t *testing.T) {
	f := oneSite()
	c := newC(f, newRec(), Options{})
	c.PollOnce(context.Background())
	f.set(func() { f.clients["s1"] = nil })
	c.PollOnce(context.Background())
	if n := len(c.Snapshot().Sites[0].Clients); n != 0 {
		t.Fatalf("clients = %d", n)
	}
}

func TestEmptyController(t *testing.T) {
	r := newRec()
	c := newC(newFake(), r, Options{})
	c.PollOnce(context.Background())
	s := c.Snapshot()
	if s == nil || len(s.Sites) != 0 || r.total() != 0 {
		t.Fatalf("snapshot=%+v errs=%v", s, r.errs)
	}
}

func fwNew(last string) device.DeviceFirmwareInfo {
	return device.DeviceFirmwareInfo{CurFwVer: sp("1.0"), LastFwVer: sp(last)}
}

func TestFirmwareMerged(t *testing.T) {
	f := oneSite()
	f.fw["aa"] = fwNew("2.0")
	c := newC(f, newRec(), Options{})
	c.PollOnce(context.Background())
	c.PollFirmware(context.Background())
	c.PollOnce(context.Background())
	u := c.Snapshot().Sites[0].Devices[0].UpgradeAvailable
	if u == nil || !*u {
		t.Fatalf("UpgradeAvailable = %v", u)
	}
}

func TestFirmwareFailureKeepsLast(t *testing.T) {
	f, r := oneSite(), newRec()
	f.fw["aa"] = fwNew("2.0")
	c := newC(f, r, Options{})
	c.PollOnce(context.Background())
	c.PollFirmware(context.Background())
	f.set(func() { f.fail["firmware"] = true })
	c.PollFirmware(context.Background())
	c.PollOnce(context.Background())
	u := c.Snapshot().Sites[0].Devices[0].UpgradeAvailable
	if u == nil || !*u {
		t.Fatalf("UpgradeAvailable = %v", u)
	}
	if r.count("firmware") != 1 {
		t.Fatalf("errs = %v", r.errs)
	}
}

func TestFirmwareBeforeSnapshot(t *testing.T) {
	f := oneSite()
	c := newC(f, newRec(), Options{})
	c.PollFirmware(context.Background())
	if f.fwCalls != 0 {
		t.Fatalf("fw calls = %d", f.fwCalls)
	}
}

func TestRunNoOverlap(t *testing.T) {
	f := oneSite()
	var active, maxActive, calls atomic.Int32
	release := make(chan struct{})
	f.sitesFn = func(ctx context.Context) ([]site.SiteSummaryInfo, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			m := maxActive.Load()
			if n <= m || maxActive.CompareAndSwap(m, n) {
				break
			}
		}
		calls.Add(1)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return f.sites, nil
	}
	c := newC(f, newRec(), Options{PollInterval: 5 * time.Millisecond, FirmwareInterval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	// Let several intervals pass while the first call is blocked.
	for calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond)
	close(release)
	for calls.Load() < 2 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
	if m := maxActive.Load(); m != 1 {
		t.Fatalf("max concurrent Sites calls = %d", m)
	}
}

func TestRunShutdownDuringFirmwareDelay(t *testing.T) {
	f := oneSite()
	f.devices["s1"] = append(f.devices["s1"], device.DeviceInfo{Mac: sp("bb")})
	f.fwCalled = make(chan struct{}, 1)
	c := newC(f, newRec(), Options{PollInterval: time.Hour, FirmwareInterval: time.Hour, FirmwareDelay: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	select {
	case <-f.fwCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("firmware never called")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return within 1s")
	}
}
