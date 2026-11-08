package collector

import (
	"testing"

	"github.com/RonaldPhilipsen/omada/cloud/client"
	"github.com/RonaldPhilipsen/omada/cloud/device"
	switchpkg "github.com/RonaldPhilipsen/omada/cloud/switch"
	"github.com/RonaldPhilipsen/omada/cloud/user"
)

func ptr[T any](v T) *T { return &v }

func TestParseUptime(t *testing.T) {
	ok := map[string]int64{
		"1 day 2 hours":        93600,
		"3 days":               259200,
		"5 minutes 7 seconds":  307,
		"10day(s) 23h 51m 36s": 10*86400 + 23*3600 + 51*60 + 36,
		"1h 14m 11s":           4451,
	}
	for in, want := range ok {
		got, valid := parseUptime(in)
		if !valid || got != want {
			t.Errorf("parseUptime(%q) = %d, %v; want %d, true", in, got, valid, want)
		}
	}
	for _, in := range []string{"", "soon", "3 fortnights", "12", "1h soon"} {
		if _, valid := parseUptime(in); valid {
			t.Errorf("parseUptime(%q) valid, want false", in)
		}
	}
}

func TestSpeedCodes(t *testing.T) {
	for code, want := range map[int32]int64{1: 10, 2: 100, 3: 1000, 4: 10000} {
		p := mapPort(switchpkg.OswPortsSettingCountersVO{PortStatus: &switchpkg.OswPortStatusVO{LinkSpeed: ptr(code)}})
		if p.SpeedMbps == nil || *p.SpeedMbps != want {
			t.Errorf("code %d: got %v, want %d", code, p.SpeedMbps, want)
		}
	}
	p := mapPort(switchpkg.OswPortsSettingCountersVO{PortStatus: &switchpkg.OswPortStatusVO{LinkSpeed: ptr(int32(9))}})
	if p.SpeedMbps != nil {
		t.Errorf("code 9: got %d, want nil", *p.SpeedMbps)
	}
}

func TestMapPortNilStatus(t *testing.T) {
	p := mapPort(switchpkg.OswPortsSettingCountersVO{
		RxBytes: ptr(int64(10)), TxBytes: ptr(int64(20)),
		RxFrames: ptr(int64(3)), TxFrames: ptr(int64(4)),
	})
	if p.RxBytes == nil || *p.RxBytes != 10 || p.TxBytes == nil || *p.TxBytes != 20 {
		t.Errorf("byte counters lost: %+v", p)
	}
	if p.RxPackets == nil || *p.RxPackets != 3 || p.TxPackets == nil || *p.TxPackets != 4 {
		t.Errorf("packet counters wrong: %+v", p)
	}
	if p.SpeedMbps != nil || p.Connected != nil || p.PoEWatts != nil {
		t.Errorf("want nil speed, connected, poe: %+v", p)
	}
}

func TestMapPortStatus(t *testing.T) {
	p := mapPort(switchpkg.OswPortsSettingCountersVO{
		Port: ptr(int32(5)), PortName: ptr("Port 5"), SwitchMac: ptr("AA"), SwitchName: ptr("sw"),
		PortStatus: &switchpkg.OswPortStatusVO{LinkStatus: ptr(int32(1)), PoePower: ptr(7.5)},
	})
	if p.Number != 5 || p.Name != "Port 5" || p.SwitchMAC != "AA" || p.SwitchName != "sw" {
		t.Errorf("identity wrong: %+v", p)
	}
	if p.Connected == nil || !*p.Connected || p.PoEWatts == nil || *p.PoEWatts != 7.5 {
		t.Errorf("status wrong: %+v", p)
	}
}

func TestMapDeviceRatio(t *testing.T) {
	d := mapDevice(device.DeviceInfo{CpuUtil: ptr(int32(42)), MemUtil: ptr(int32(50))})
	if d.CPURatio == nil || *d.CPURatio != 0.42 {
		t.Errorf("cpu ratio = %v, want 0.42", d.CPURatio)
	}
	if d.MemRatio == nil || *d.MemRatio != 0.5 {
		t.Errorf("mem ratio = %v, want 0.5", d.MemRatio)
	}
	d = mapDevice(device.DeviceInfo{})
	if d.CPURatio != nil || d.MemRatio != nil || d.UptimeSeconds != nil || d.Status != nil {
		t.Errorf("want nils: %+v", d)
	}
	if d.UpgradeAvailable != nil {
		t.Error("UpgradeAvailable must stay nil")
	}
}

func TestMapDeviceUptimeStatus(t *testing.T) {
	d := mapDevice(device.DeviceInfo{Uptime: ptr("3 days"), Status: ptr(int32(1))})
	if d.UptimeSeconds == nil || *d.UptimeSeconds != 259200 {
		t.Errorf("uptime = %v", d.UptimeSeconds)
	}
	if d.Status == nil || *d.Status != 1 {
		t.Errorf("status = %v", d.Status)
	}
	d = mapDevice(device.DeviceInfo{Uptime: ptr("soon")})
	if d.UptimeSeconds != nil {
		t.Error("unparsable uptime must be nil")
	}
}

func TestUpgradeAvailable(t *testing.T) {
	cases := []struct {
		name string
		in   device.DeviceFirmwareInfo
		want bool
	}{
		{"newer", device.DeviceFirmwareInfo{CurFwVer: ptr("1"), LastFwVer: ptr("2")}, true},
		{"same", device.DeviceFirmwareInfo{CurFwVer: ptr("1"), LastFwVer: ptr("1")}, false},
		{"empty last", device.DeviceFirmwareInfo{CurFwVer: ptr("1"), LastFwVer: ptr("")}, false},
		{"nil last", device.DeviceFirmwareInfo{CurFwVer: ptr("1")}, false},
	}
	for _, c := range cases {
		got := upgradeAvailable(c.in)
		if got == nil || *got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestMapClientWiredWireless(t *testing.T) {
	for _, w := range []*bool{nil, ptr(false)} {
		c := mapClient(client.OpenApiClientInfo{Wireless: w, Rssi: ptr(int32(-50)), Ssid: ptr("x"), ApName: ptr("ap")})
		if c.Wireless || c.RSSI != nil || c.SSID != "" || c.APName != "" {
			t.Errorf("wired client wrong: %+v", c)
		}
	}
	c := mapClient(client.OpenApiClientInfo{
		Wireless: ptr(true), Rssi: ptr(int32(-60)), Ssid: ptr("home"), ApName: ptr("ap1"),
		Uptime: ptr(int64(9)), TrafficUp: ptr(int64(1)), TrafficDown: ptr(int64(2)), Activity: ptr(int64(3)),
	})
	if !c.Wireless || c.SSID != "home" || c.APName != "ap1" || c.RSSI == nil || *c.RSSI != -60 {
		t.Errorf("wireless client wrong: %+v", c)
	}
	if *c.UptimeSeconds != 9 || *c.TrafficUp != 1 || *c.TrafficDown != 2 || *c.Activity != 3 {
		t.Errorf("counters wrong: %+v", c)
	}
}

func TestMapControllerStorage(t *testing.T) {
	c := mapController(user.ControllerStatus{
		UpTime: ptr(int64(975468000)),
		HwcStorage: &[]user.DiskCondition{
			{Name: ptr("disk"), TotalStorage: ptr(2.5), UsedStorage: ptr(1.0)},
		},
	})
	if c.UptimeSeconds == nil || *c.UptimeSeconds != 975468 {
		t.Errorf("uptime = %v", c.UptimeSeconds)
	}
	if len(c.Storage) != 1 || c.Storage[0].Name != "disk" ||
		c.Storage[0].CapacityBytes == nil || *c.Storage[0].CapacityBytes != 2.5e9 ||
		c.Storage[0].UsedBytes == nil || *c.Storage[0].UsedBytes != 1e9 {
		t.Errorf("storage wrong: %+v", c.Storage)
	}
	c = mapController(user.ControllerStatus{})
	if c.Storage == nil || len(c.Storage) != 0 {
		t.Errorf("want empty non-nil slice, got %#v", c.Storage)
	}
}

func TestMapName(t *testing.T) {
	if d := mapDevice(device.DeviceInfo{Mac: ptr("AA-BB")}); d.Name != "AA-BB" {
		t.Errorf("device name = %q", d.Name)
	}
	if c := mapClient(client.OpenApiClientInfo{Mac: ptr("CC-DD")}); c.Name != "CC-DD" {
		t.Errorf("client name = %q", c.Name)
	}
	if d := mapDevice(device.DeviceInfo{Mac: ptr("AA"), Name: ptr("ap")}); d.Name != "ap" {
		t.Errorf("device name = %q", d.Name)
	}
}

func TestPortConnectedOnlyWhenLinkStatusOne(t *testing.T) {
	for code, want := range map[int32]bool{0: false, 1: true, 2: false} {
		p := mapPort(switchpkg.OswPortsSettingCountersVO{PortStatus: &switchpkg.OswPortStatusVO{LinkStatus: ptr(code)}})
		if p.Connected == nil || *p.Connected != want {
			t.Errorf("LinkStatus %d: got %v, want %v", code, p.Connected, want)
		}
	}
}

func TestMapPortUnit(t *testing.T) {
	p := mapPort(switchpkg.OswPortsSettingCountersVO{Port: ptr(int32(3)), Unit: ptr(int32(2))})
	if p.Unit == nil || *p.Unit != 2 {
		t.Errorf("Unit = %v", p.Unit)
	}
	if q := mapPort(switchpkg.OswPortsSettingCountersVO{Port: ptr(int32(3))}); q.Unit != nil {
		t.Errorf("Unit = %v, want nil", *q.Unit)
	}
}
