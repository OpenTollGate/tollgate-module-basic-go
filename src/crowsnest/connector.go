package crowsnest

// Connector defines the interface for getting connection information.
type Connector interface {
	GetConnectedSSID() (string, error)
}