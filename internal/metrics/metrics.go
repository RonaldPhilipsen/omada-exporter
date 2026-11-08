// Package metrics registers the OpenTelemetry instruments that report the
// controller snapshot and the exporter's own poll statistics.
package metrics

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/RonaldPhilipsen/omada-exporter/internal/snapshot"
)

// Recorder records poll self-metrics. It satisfies collector.Recorder.
type Recorder struct {
	errors   metric.Int64Counter
	duration metric.Float64Gauge
}

// PollError counts one failed poll section.
func (r *Recorder) PollError(section string) {
	r.errors.Add(context.Background(), 1,
		metric.WithAttributes(attribute.String("section", section)))
}

// PollDuration records the length of the last poll.
func (r *Recorder) PollDuration(d time.Duration) {
	r.duration.Record(context.Background(), d.Seconds())
}

// Register creates all instruments on m. Observable instruments read the
// snapshot from src once per collection. If clientMetrics is false, the
// omada.client.* instruments are not created.
func Register(m metric.Meter, src func() *snapshot.Snapshot, clientMetrics bool) (*Recorder, error) {
	var (
		err  error
		inst []metric.Observable
	)
	// Each helper keeps the first error and reuses it for later calls.
	i64g := func(name, unit string) metric.Int64Observable {
		if err != nil {
			return nil
		}
		var o metric.Int64ObservableGauge
		o, err = m.Int64ObservableGauge(name, metric.WithUnit(unit))
		inst = append(inst, o)
		return o
	}
	f64g := func(name, unit string) metric.Float64Observable {
		if err != nil {
			return nil
		}
		var o metric.Float64ObservableGauge
		o, err = m.Float64ObservableGauge(name, metric.WithUnit(unit))
		inst = append(inst, o)
		return o
	}
	i64c := func(name, unit string) metric.Int64Observable {
		if err != nil {
			return nil
		}
		var o metric.Int64ObservableCounter
		o, err = m.Int64ObservableCounter(name, metric.WithUnit(unit))
		inst = append(inst, o)
		return o
	}

	ctrlUptime := i64g("omada.controller.uptime", "s")
	storUsed := f64g("omada.controller.storage.used", "By")
	storCap := f64g("omada.controller.storage.capacity", "By")
	devCPU := f64g("omada.device.cpu.utilization", "1")
	devMem := f64g("omada.device.memory.utilization", "1")
	devStatus := i64g("omada.device.status", "1")
	devUptime := i64g("omada.device.uptime", "s")
	devUpgrade := i64g("omada.device.upgrade.available", "1")
	portIO := i64c("omada.switch.port.io", "By")
	portPkts := i64c("omada.switch.port.packets", "{packet}")
	portErrs := i64c("omada.switch.port.errors", "{error}")
	portSpeed := i64g("omada.switch.port.speed", "Mbit/s")
	portUp := i64g("omada.switch.port.connected", "1")
	portPoE := f64g("omada.switch.port.poe.power", "W")
	siteClients := i64g("omada.site.clients", "{client}")

	var cliSignal, cliUptime, cliTraffic, cliActivity metric.Int64Observable
	if clientMetrics {
		cliSignal = i64g("omada.client.signal.strength", "dBm")
		cliUptime = i64g("omada.client.uptime", "s")
		cliTraffic = i64c("omada.client.traffic", "By")
		cliActivity = i64g("omada.client.activity", "By/s")
	}
	if err != nil {
		return nil, err
	}

	rec := &Recorder{}
	if rec.errors, err = m.Int64Counter("omada.exporter.scrape.errors", metric.WithUnit("{error}")); err != nil {
		return nil, err
	}
	if rec.duration, err = m.Float64Gauge("omada.exporter.scrape.duration", metric.WithUnit("s")); err != nil {
		return nil, err
	}

	_, err = m.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		s := src()
		if s == nil {
			return nil
		}
		if c := s.Controller; c != nil {
			obsI(o, ctrlUptime, c.UptimeSeconds)
			for _, st := range c.Storage {
				a := metric.WithAttributes(attribute.String("storage.name", st.Name))
				obsF(o, storUsed, st.UsedBytes, a)
				obsF(o, storCap, st.CapacityBytes, a)
			}
		}
		for _, site := range s.Sites {
			sn := attribute.String("omada.site.name", site.Name)
			for _, d := range site.Devices {
				a := metric.WithAttributes(sn,
					attribute.String("device.mac", d.MAC),
					attribute.String("device.name", d.Name),
					attribute.String("device.model", d.Model),
					attribute.String("device.type", d.Type))
				obsF(o, devCPU, d.CPURatio, a)
				obsF(o, devMem, d.MemRatio, a)
				obsI(o, devStatus, d.Status, a)
				obsI(o, devUptime, d.UptimeSeconds, a)
				obsB(o, devUpgrade, d.UpgradeAvailable, a)
			}
			for _, p := range site.Ports {
				base := []attribute.KeyValue{sn,
					attribute.String("switch.mac", p.SwitchMAC),
					attribute.String("switch.name", p.SwitchName),
					attribute.Int64("port.number", p.Number),
					attribute.String("port.name", p.Name)}
				if p.Unit != nil {
					base = append(base, attribute.Int64("port.unit", *p.Unit))
				}
				a := metric.WithAttributes(base...)
				tx, rx := dirAttrs(base)
				obsI(o, portIO, p.TxBytes, tx)
				obsI(o, portIO, p.RxBytes, rx)
				obsI(o, portPkts, p.TxPackets, tx)
				obsI(o, portPkts, p.RxPackets, rx)
				obsI(o, portErrs, p.TxErrors, tx)
				obsI(o, portErrs, p.RxErrors, rx)
				obsI(o, portSpeed, p.SpeedMbps, a)
				obsB(o, portUp, p.Connected, a)
				obsF(o, portPoE, p.PoEWatts, a)
			}
			var wired, wireless int64
			for _, c := range site.Clients {
				if c.Wireless {
					wireless++
				} else {
					wired++
				}
				if !clientMetrics {
					continue
				}
				base := []attribute.KeyValue{sn,
					attribute.String("client.mac", c.MAC),
					attribute.String("client.name", c.Name),
					attribute.String("client.connection", connection(c.Wireless))}
				if c.Wireless {
					base = append(base,
						attribute.String("client.ssid", c.SSID),
						attribute.String("client.ap.name", c.APName))
				}
				a := metric.WithAttributes(base...)
				tx, rx := dirAttrs(base)
				obsI(o, cliSignal, c.RSSI, a)
				obsI(o, cliUptime, c.UptimeSeconds, a)
				obsI(o, cliTraffic, c.TrafficUp, tx)
				obsI(o, cliTraffic, c.TrafficDown, rx)
				obsI(o, cliActivity, c.Activity, a)
			}
			for conn, n := range map[string]int64{"wired": wired, "wireless": wireless} {
				o.ObserveInt64(siteClients, n,
					metric.WithAttributes(sn, attribute.String("client.connection", conn)))
			}
		}
		return nil
	}, inst...)
	if err != nil {
		return nil, err
	}
	return rec, nil
}

func connection(wireless bool) string {
	if wireless {
		return "wireless"
	}
	return "wired"
}

// dirAttrs returns attribute options for the transmit and receive directions.
func dirAttrs(base []attribute.KeyValue) (tx, rx metric.MeasurementOption) {
	mk := func(dir string) metric.MeasurementOption {
		kvs := append(append([]attribute.KeyValue(nil), base...), attribute.String("direction", dir))
		return metric.WithAttributes(kvs...)
	}
	return mk("transmit"), mk("receive")
}

func obsI(o metric.Observer, i metric.Int64Observable, v *int64, opts ...metric.ObserveOption) {
	if v != nil {
		o.ObserveInt64(i, *v, opts...)
	}
}

func obsF(o metric.Observer, i metric.Float64Observable, v *float64, opts ...metric.ObserveOption) {
	if v != nil {
		o.ObserveFloat64(i, *v, opts...)
	}
}

func obsB(o metric.Observer, i metric.Int64Observable, v *bool, opts ...metric.ObserveOption) {
	if v == nil {
		return
	}
	n := int64(0)
	if *v {
		n = 1
	}
	o.ObserveInt64(i, n, opts...)
}
