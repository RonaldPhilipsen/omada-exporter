package collector

import (
	"regexp"
	"strconv"

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
	out := snapshot.Controller{Storage: []snapshot.Storage{}}
	if c.UpTime != nil {
		// The OpenAPI specification says seconds, but controllers send milliseconds.
		s := *c.UpTime / 1000
		out.UptimeSeconds = &s
	}
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

var (
	uptimeFormat = regexp.MustCompile(`^\s*(?:\d+\s*(?:day\(s\)|days?|hours?|minutes?|seconds?|h|m|s)\s*)+$`)
	uptimePart   = regexp.MustCompile(`(\d+)\s*(day|hour|h|minute|m|second|s)`)
)

// parseUptime reads controller uptime text such as "10day(s) 23h 51m 36s".
func parseUptime(s string) (int64, bool) {
	if !uptimeFormat.MatchString(s) {
		return 0, false
	}
	var total int64
	for _, m := range uptimePart.FindAllStringSubmatch(s, -1) {
		n, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			return 0, false
		}
		switch m[2] {
		case "day":
			total += n * 86400
		case "hour", "h":
			total += n * 3600
		case "minute", "m":
			total += n * 60
		default:
			total += n
		}
	}
	return total, true
}
