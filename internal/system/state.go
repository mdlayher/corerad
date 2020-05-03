package system

type State interface {
	IPv6Autoconf(iface string) (bool, error)
	IPv6Forwarding(iface string) (bool, error)
}

func NewState() State { return systemState{} }

type systemState struct{}

func (systemState) IPv6Autoconf(iface string) (bool, error) {
	return getIPv6Autoconf(iface)
}

func (systemState) IPv6Forwarding(iface string) (bool, error) {
	return getIPv6Forwarding(iface)
}
