// Package snapshot holds the controller state captured in one poll.
// Optional values are pointers so that missing data is not reported as zero.
package snapshot

type Snapshot struct {
	Controller *Controller
	Sites      []Site
}

type Controller struct {
	UptimeSeconds *int64
	Storage       []Storage
}

type Storage struct {
	Name          string
	UsedBytes     *float64
	CapacityBytes *float64
}

type Site struct {
	ID, Name string
	Devices  []Device
	Ports    []Port
	Clients  []Client
}

type Device struct {
	MAC, Name, Model, Type string
	CPURatio, MemRatio     *float64
	Status                 *int64
	UptimeSeconds          *int64
	UpgradeAvailable       *bool
}

type Port struct {
	SwitchMAC, SwitchName, Name string
	Number                      int64
	RxBytes, TxBytes            *int64
	RxPackets, TxPackets        *int64
	RxErrors, TxErrors          *int64
	SpeedMbps                   *int64
	Connected                   *bool
	PoEWatts                    *float64
}

type Client struct {
	MAC, Name, SSID, APName string
	Wireless                bool
	RSSI                    *int64
	UptimeSeconds           *int64
	TrafficUp, TrafficDown  *int64
	Activity                *int64
}
