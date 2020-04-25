// Package crtest provides testing facilities for CoreRAD.
package crtest

import (
	"fmt"

	"inet.af/netaddr"
)

// MustIP parses a netaddr.IP from s or panics.
func MustIP(s string) netaddr.IP {
	ip, err := netaddr.ParseIP(s)
	if err != nil {
		panicf("crtest: failed to parse IP address: %v", err)
	}

	return ip
}

// MustIPPrefix parses a netaddr.IPPrefix from s or panics.
func MustIPPrefix(s string) netaddr.IPPrefix {
	p, err := netaddr.ParseIPPrefix(s)
	if err != nil {
		panicf("crtest: failed to parse IP prefix: %v", err)
	}

	return p
}

func panicf(format string, a ...interface{}) {
	panic(fmt.Sprintf(format, a...))
}
