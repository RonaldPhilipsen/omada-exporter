package collector

import (
	"strconv"
	"strings"

	"github.com/RonaldPhilipsen/omada/cloud/client"
	"github.com/RonaldPhilipsen/omada/cloud/device"
	switchpkg "github.com/RonaldPhilipsen/omada/cloud/switch"
	"github.com/RonaldPhilipsen/omada/cloud/user"

	"github.com/RonaldPhilipsen/omada-exporter/internal/snapshot"
)

const bytesPerGB = 1e9

func str(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func i64(v *int32) *int64 {
	if v == nil {
		return nil
	}
	r := int64(*v)
	return &r
}

func ratio(pct *int32) *float64 {
	if pct == nil {
		return nil
	}
	r := float64(*pct) / 100
	return &r
}

func scaleGB(v *float64) *float64 {
	if v == nil {
		return nil
	}
	r := *v * bytesPerGB
	return &r
}

func mapController(c user.ControllerStatus) snapshot.Controller {
	out := snapshot.Controller{UptimeSeconds: c.UpTime, Storage: []snapshot.Storage{}}
	if c.HwcStorage != nil {
		for _, d := range *c.HwcStorage {
			out.Storage = append(out.Storage, snapshot.Storage{
				Name:          str(d.Name),
				UsedBytes:     scaleGB(d.UsedStorage),
				CapacityBytes: scaleGB(d.TotalStorage),
			})
		}
	}
	return out
}

func mapDevice(d device.DeviceInfo) snapshot.Device {
	out := snapshot.Device{
		MAC:      str(d.Mac),
		Name:     str(d.Name),
		Model:    str(d.Model),
		Type:     str(d.Type),
		CPURatio: ratio(d.CpuUtil),
		MemRatio: ratio(d.MemUtil),
		Status:   i64(d.Status),
	}
	if out.Name == "" {
		out.Name = out.MAC
	}
	if d.Uptime != nil {
		if s, ok := parseUptime(*d.Uptime); ok {
			out.UptimeSeconds = &s
		}
	}
	return out
}

// speedMbps maps the Omada link speed code to Mbps.
var speedMbps = map[int32]int64{1: 10, 2: 100, 3: 1000, 4: 10000}

func mapPort(p switchpkg.OswPortsSettingCountersVO) snapshot.Port {
	out := snapshot.Port{
		SwitchMAC:  str(p.SwitchMac),
		SwitchName: str(p.SwitchName),
		Name:       str(p.PortName),
		RxBytes:    p.RxBytes,
		TxBytes:    p.TxBytes,
		RxPackets:  p.RxFrames,
		TxPackets:  p.TxFrames,
		RxErrors:   p.RxErrors,
		TxErrors:   p.TxErrors,
	}
	if p.Port != nil {
		out.Number = int64(*p.Port)
	}
	if p.Unit != nil {
		u := int64(*p.Unit)
		out.Unit = &u
	}
	if st := p.PortStatus; st != nil {
		if st.LinkSpeed != nil {
			if v, ok := speedMbps[*st.LinkSpeed]; ok {
				out.SpeedMbps = &v
			}
		}
		if st.LinkStatus != nil {
			c := *st.LinkStatus == 1
			out.Connected = &c
		}
		out.PoEWatts = st.PoePower
	}
	return out
}

func mapClient(c client.OpenApiClientInfo) snapshot.Client {
	out := snapshot.Client{
		MAC:           str(c.Mac),
		Name:          str(c.Name),
		UptimeSeconds: c.Uptime,
		TrafficUp:     c.TrafficUp,
		TrafficDown:   c.TrafficDown,
		Activity:      c.Activity,
	}
	if out.Name == "" {
		out.Name = out.MAC
	}
	if c.Wireless != nil && *c.Wireless {
		out.Wireless = true
		out.SSID = str(c.Ssid)
		out.APName = str(c.ApName)
		out.RSSI = i64(c.Rssi)
	}
	return out
}

func upgradeAvailable(f device.DeviceFirmwareInfo) *bool {
	r := f.LastFwVer != nil && *f.LastFwVer != "" && str(f.CurFwVer) != *f.LastFwVer
	return &r
}

// parseUptime reads "<number> <unit>" pairs such as "1 day 2 hours".
func parseUptime(s string) (int64, bool) {
	f := strings.Fields(s)
	if len(f) == 0 || len(f)%2 != 0 {
		return 0, false
	}
	var total int64
	for i := 0; i < len(f); i += 2 {
		n, err := strconv.ParseInt(f[i], 10, 64)
		if err != nil || n < 0 {
			return 0, false
		}
		var unit int64
		switch strings.TrimSuffix(f[i+1], "s") {
		case "day":
			unit = 86400
		case "hour":
			unit = 3600
		case "minute":
			unit = 60
		case "second":
			unit = 1
		default:
			return 0, false
		}
		total += n * unit
	}
	return total, true
}
