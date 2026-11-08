package metrics

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/RonaldPhilipsen/omada-exporter/internal/snapshot"
)

func ptr[T any](v T) *T { return &v }

func fixture() *snapshot.Snapshot {
	return &snapshot.Snapshot{
		Controller: &snapshot.Controller{
			UptimeSeconds: ptr(int64(100)),
			Storage: []snapshot.Storage{
				{Name: "root", UsedBytes: ptr(1.0), CapacityBytes: ptr(2.0)},
			},
		},
		Sites: []snapshot.Site{{
			ID: "s1", Name: "Home",
			Devices: []snapshot.Device{
				{MAC: "aa", Name: "ap1", Model: "EAP", Type: "ap",
					CPURatio: ptr(0.5), MemRatio: ptr(0.4), Status: ptr(int64(1)),
					UptimeSeconds: ptr(int64(60)), UpgradeAvailable: ptr(true)},
				{MAC: "bb", Name: "sw1", Model: "SG", Type: "switch",
					MemRatio: ptr(0.3)},
			},
			Ports: []snapshot.Port{{
				SwitchMAC: "bb", SwitchName: "sw1", Name: "Port1", Number: 1,
				RxBytes: ptr(int64(10)), TxBytes: ptr(int64(20)),
				RxPackets: ptr(int64(1)), TxPackets: ptr(int64(2)),
				RxErrors: ptr(int64(0)), TxErrors: ptr(int64(0)),
				SpeedMbps: ptr(int64(1000)), Connected: ptr(true), PoEWatts: ptr(3.5),
			}},
			Clients: []snapshot.Client{
				{MAC: "c1", Name: "laptop", Wireless: true, SSID: "wifi", APName: "ap1",
					RSSI: ptr(int64(-50)), UptimeSeconds: ptr(int64(5)),
					TrafficUp: ptr(int64(7)), TrafficDown: ptr(int64(8)), Activity: ptr(int64(9))},
				{MAC: "c2", Name: "pc", UptimeSeconds: ptr(int64(6)),
					TrafficUp: ptr(int64(1)), TrafficDown: ptr(int64(2)), Activity: ptr(int64(3))},
			},
		}},
	}
}

func collect(t *testing.T, snap *snapshot.Snapshot, clientMetrics bool) (map[string]metricdata.Metrics, *Recorder, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	rec, err := Register(mp.Meter("test"), func() *snapshot.Snapshot { return snap }, clientMetrics)
	if err != nil {
		t.Fatal(err)
	}
	return read(t, reader), rec, reader
}

func read(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Metrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	out := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			out[m.Name] = m
		}
	}
	return out
}

type point struct {
	attrs attribute.Set
	value float64
}

func points(m metricdata.Metrics) []point {
	var out []point
	switch d := m.Data.(type) {
	case metricdata.Gauge[int64]:
		for _, p := range d.DataPoints {
			out = append(out, point{p.Attributes, float64(p.Value)})
		}
	case metricdata.Gauge[float64]:
		for _, p := range d.DataPoints {
			out = append(out, point{p.Attributes, p.Value})
		}
	case metricdata.Sum[int64]:
		for _, p := range d.DataPoints {
			out = append(out, point{p.Attributes, float64(p.Value)})
		}
	case metricdata.Sum[float64]:
		for _, p := range d.DataPoints {
			out = append(out, point{p.Attributes, p.Value})
		}
	}
	return out
}

func keys(s attribute.Set) map[string]bool {
	out := map[string]bool{}
	for _, kv := range s.ToSlice() {
		out[string(kv.Key)] = true
	}
	return out
}

func TestInstrumentNamesAndUnits(t *testing.T) {
	got, rec, reader := collect(t, fixture(), true)
	rec.PollError("ports")
	rec.PollDuration(time.Second)
	got = read(t, reader)

	const (
		gauge   = "gauge"
		counter = "counter"
	)
	want := []struct{ name, typ, unit string }{
		{"omada.controller.uptime", gauge, "s"},
		{"omada.controller.storage.used", gauge, "By"},
		{"omada.controller.storage.capacity", gauge, "By"},
		{"omada.device.cpu.utilization", gauge, "1"},
		{"omada.device.memory.utilization", gauge, "1"},
		{"omada.device.status", gauge, "1"},
		{"omada.device.uptime", gauge, "s"},
		{"omada.device.upgrade.available", gauge, "1"},
		{"omada.switch.port.io", counter, "By"},
		{"omada.switch.port.packets", counter, "{packet}"},
		{"omada.switch.port.errors", counter, "{error}"},
		{"omada.switch.port.speed", gauge, "Mbit/s"},
		{"omada.switch.port.connected", gauge, "1"},
		{"omada.switch.port.poe.power", gauge, "W"},
		{"omada.site.clients", gauge, "{client}"},
		{"omada.client.signal.strength", gauge, "dBm"},
		{"omada.client.uptime", gauge, "s"},
		{"omada.client.traffic", counter, "By"},
		{"omada.client.activity", gauge, "By/s"},
		{"omada.exporter.scrape.errors", counter, "{error}"},
		{"omada.exporter.scrape.duration", gauge, "s"},
	}
	if len(want) != 21 {
		t.Fatalf("table has %d rows", len(want))
	}
	for _, w := range want {
		m, ok := got[w.name]
		if !ok {
			t.Errorf("%s missing", w.name)
			continue
		}
		if m.Unit != w.unit {
			t.Errorf("%s unit = %q, want %q", w.name, m.Unit, w.unit)
		}
		var isGauge, isMonoSum bool
		switch d := m.Data.(type) {
		case metricdata.Gauge[int64], metricdata.Gauge[float64]:
			isGauge = true
		case metricdata.Sum[int64]:
			isMonoSum = d.IsMonotonic
		case metricdata.Sum[float64]:
			isMonoSum = d.IsMonotonic
		}
		if w.typ == gauge && !isGauge || w.typ == counter && !isMonoSum {
			t.Errorf("%s has wrong type %T", w.name, m.Data)
		}
	}
}

func TestDeviceAttributes(t *testing.T) {
	got, _, _ := collect(t, fixture(), true)
	ps := points(got["omada.device.cpu.utilization"])
	if len(ps) != 1 {
		t.Fatalf("points = %d", len(ps))
	}
	want := map[string]bool{"omada.site.name": true, "device.mac": true, "device.name": true, "device.model": true, "device.type": true}
	k := keys(ps[0].attrs)
	if len(k) != len(want) {
		t.Fatalf("attrs = %v", k)
	}
	for a := range want {
		if !k[a] {
			t.Errorf("missing %s", a)
		}
	}
	if ps[0].value != 0.5 {
		t.Errorf("value = %v", ps[0].value)
	}
}

func TestPortDirection(t *testing.T) {
	got, _, _ := collect(t, fixture(), true)
	dirs := map[string]float64{}
	for _, p := range points(got["omada.switch.port.io"]) {
		d, _ := p.attrs.Value("direction")
		dirs[d.AsString()] = p.value
		if n, ok := p.attrs.Value("port.number"); !ok || n.Type() != attribute.INT64 {
			t.Errorf("port.number = %v", n)
		}
	}
	if len(dirs) != 2 || dirs["transmit"] != 20 || dirs["receive"] != 10 {
		t.Errorf("dirs = %v", dirs)
	}
}

func TestWiredClientAttributes(t *testing.T) {
	got, _, _ := collect(t, fixture(), true)
	for _, p := range points(got["omada.client.uptime"]) {
		mac, _ := p.attrs.Value("client.mac")
		_, hasSSID := p.attrs.Value("client.ssid")
		_, hasAP := p.attrs.Value("client.ap.name")
		conn, _ := p.attrs.Value("client.connection")
		switch mac.AsString() {
		case "c2":
			if hasSSID || hasAP || conn.AsString() != "wired" {
				t.Errorf("wired attrs wrong: %v", p.attrs.ToSlice())
			}
		case "c1":
			if !hasSSID || !hasAP || conn.AsString() != "wireless" {
				t.Errorf("wireless attrs wrong: %v", p.attrs.ToSlice())
			}
		}
	}
	sig := points(got["omada.client.signal.strength"])
	if len(sig) != 1 {
		t.Fatalf("signal points = %d", len(sig))
	}
	if mac, _ := sig[0].attrs.Value("client.mac"); mac.AsString() != "c1" {
		t.Errorf("signal for %v", mac)
	}
}

func TestClientMetricsOff(t *testing.T) {
	got, _, _ := collect(t, fixture(), false)
	for name := range got {
		if len(name) >= 13 && name[:13] == "omada.client." {
			t.Errorf("unexpected %s", name)
		}
	}
	counts := map[string]float64{}
	for _, p := range points(got["omada.site.clients"]) {
		c, _ := p.attrs.Value("client.connection")
		counts[c.AsString()] = p.value
	}
	if counts["wired"] != 1 || counts["wireless"] != 1 || len(counts) != 2 {
		t.Errorf("counts = %v", counts)
	}
}

func TestSiteClientsZero(t *testing.T) {
	s := fixture()
	s.Sites[0].Clients = nil
	got, _, _ := collect(t, s, true)
	ps := points(got["omada.site.clients"])
	if len(ps) != 2 || ps[0].value != 0 || ps[1].value != 0 {
		t.Errorf("points = %v", ps)
	}
}

func TestNilSnapshot(t *testing.T) {
	got, _, _ := collect(t, nil, true)
	for name, m := range got {
		if len(name) > 6 && name[:6] == "omada." && len(points(m)) > 0 {
			t.Errorf("%s has points", name)
		}
	}
}

func TestAbsentValues(t *testing.T) {
	got, _, _ := collect(t, fixture(), true)
	if n := len(points(got["omada.device.cpu.utilization"])); n != 1 {
		t.Errorf("cpu points = %d", n)
	}
	if n := len(points(got["omada.device.memory.utilization"])); n != 2 {
		t.Errorf("mem points = %d", n)
	}
	if n := len(points(got["omada.device.upgrade.available"])); n != 1 {
		t.Errorf("upgrade points = %d", n)
	}
}

func TestBoolValues(t *testing.T) {
	s := fixture()
	s.Sites[0].Devices[0].UpgradeAvailable = ptr(false)
	got, _, _ := collect(t, s, true)
	if ps := points(got["omada.device.upgrade.available"]); len(ps) != 1 || ps[0].value != 0 {
		t.Errorf("points = %v", ps)
	}
	if ps := points(got["omada.switch.port.connected"]); len(ps) != 1 || ps[0].value != 1 {
		t.Errorf("points = %v", ps)
	}
}

func TestControllerHasNoSite(t *testing.T) {
	got, _, _ := collect(t, fixture(), true)
	ps := points(got["omada.controller.uptime"])
	if len(ps) != 1 {
		t.Fatalf("points = %d", len(ps))
	}
	if _, ok := ps[0].attrs.Value("omada.site.name"); ok {
		t.Error("controller has omada.site.name")
	}
	for _, p := range points(got["omada.controller.storage.used"]) {
		if _, ok := p.attrs.Value("storage.name"); !ok {
			t.Error("storage.name missing")
		}
	}
}

func TestRecorder(t *testing.T) {
	_, rec, reader := collect(t, fixture(), true)
	rec.PollError("ports")
	rec.PollDuration(1500 * time.Millisecond)
	got := read(t, reader)
	ps := points(got["omada.exporter.scrape.errors"])
	if len(ps) != 1 || ps[0].value != 1 {
		t.Fatalf("errors = %v", ps)
	}
	if s, _ := ps[0].attrs.Value("section"); s.AsString() != "ports" {
		t.Errorf("section = %v", s)
	}
	d := points(got["omada.exporter.scrape.duration"])
	if len(d) != 1 || d[0].value != 1.5 {
		t.Errorf("duration = %v", d)
	}
}
