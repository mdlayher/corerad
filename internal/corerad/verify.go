// Copyright 2020 Matt Layher
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package corerad

import (
	"time"

	"github.com/mdlayher/ndp"
)

// verifyRAs checks for consistency between two router advertisements.
func verifyRAs(a, b *ndp.RouterAdvertisement) bool {
	// Verify using the rules established in:
	// https://tools.ietf.org/html/rfc4861#section-6.2.7.
	//
	// TODO: more verbose error reporting? Individual fields?
	return a.CurrentHopLimit == b.CurrentHopLimit &&
		a.ManagedConfiguration == b.ManagedConfiguration &&
		a.OtherConfiguration == b.OtherConfiguration &&
		durationsConsistent(a.ReachableTime, b.ReachableTime) &&
		durationsConsistent(a.RetransmitTimer, b.RetransmitTimer) &&
		mtuConsistent(a.Options, b.Options) &&
		prefixesConsistent(a.Options, b.Options) &&
		routesConsistent(a.Options, b.Options) &&
		rdnssConsistent(a.Options, b.Options) &&
		dnsslConsistent(a.Options, b.Options)
}

// durationsConsistent reports whether two time.Duration values are consistent.
func durationsConsistent(want, got time.Duration) bool {
	if want == 0 || got == 0 {
		// If either duration is unspecified, nothing to do.
		return true
	}

	return want == got
}

// mtuConsistent reports whether two NDP MTU option values exist, and if so,
// if they are consistent.
func mtuConsistent(want, got []ndp.Option) bool {
	mtuA, okA := pickMTU(want)
	mtuB, okB := pickMTU(got)

	if !okA || !okB {
		// If either are not advertising MTU, nothing to do.
		return true
	}

	return mtuA == mtuB
}

// prefixesConsistent reports whether two NDP prefix information option values
// exist, and if so, if they are consistent.
func prefixesConsistent(want, got []ndp.Option) bool {
	pfxA := pickPrefixes(want)
	pfxB := pickPrefixes(got)

	if len(pfxA) == 0 || len(pfxB) == 0 {
		// If either are advertising no prefixes, nothing to do.
		return true
	}

	for _, a := range pfxA {
		for _, b := range pfxB {
			if !a.Prefix.Equal(b.Prefix) || a.PrefixLength != b.PrefixLength {
				// a and b don't match, don't compare them.
				continue
			}

			// Matching prefix, verify its lifetimes.
			//
			// TODO: deal with decrementing lifetimes? CoreRAD doesn't support
			// them at the moment so we can't verify them either.
			if a.PreferredLifetime != b.PreferredLifetime || a.ValidLifetime != b.ValidLifetime {
				return false
			}
		}
	}

	return true
}

// routesConsistent reports whether two NDP route information option values
// exist, and if so, if they are consistent.
func routesConsistent(want, got []ndp.Option) bool {
	pfxA := pickRoutes(want)
	pfxB := pickRoutes(got)

	if len(pfxA) == 0 || len(pfxB) == 0 {
		// If either are advertising no routes, nothing to do.
		return true
	}

	for _, a := range pfxA {
		for _, b := range pfxB {
			if !a.Prefix.Equal(b.Prefix) || a.PrefixLength != b.PrefixLength {
				// a and b don't match, don't compare them.
				continue
			}

			// Matching prefix, verify its lifetimes assuming that the preference
			// values are the same. This would indicate a potential flapping
			// configuration, where different preferences would cause the client
			// to resolve that conflict on its own.
			//
			// TODO: check that this logic is sound.
			//
			// TODO: deal with decrementing lifetimes? CoreRAD doesn't support
			// them at the moment so we can't verify them either.
			if a.Preference == b.Preference && a.RouteLifetime != b.RouteLifetime {
				return false
			}
		}
	}

	return true
}

// rdnssConsistent reports whether two NDP RDNSS option values exist, and if so,
// if they are consistent.
func rdnssConsistent(want, got []ndp.Option) bool {
	a := pickRDNSS(want)
	b := pickRDNSS(got)

	if len(a) == 0 || len(b) == 0 {
		// If either are advertising no RDNSS, nothing to do.
		return true
	}

	if len(a) != len(b) {
		// Inconsistent number of servers.
		return false
	}

	// Assuming both are advertising RDNSS, the options must be identical.
	for i := range a {
		if len(a[i].Servers) != len(b[i].Servers) {
			return false
		}

		for j := range a[i].Servers {
			if !a[i].Servers[j].Equal(b[i].Servers[j]) {
				return false
			}
		}

		if a[i].Lifetime != b[i].Lifetime {
			return false
		}
	}

	return true
}

// dnsslConsistent reports whether two NDP DNSSL option values exist, and if so,
// if they are consistent.
func dnsslConsistent(want, got []ndp.Option) bool {
	a := pickDNSSL(want)
	b := pickDNSSL(got)

	if len(a) == 0 || len(b) == 0 {
		// If either are advertising no DNSSL, nothing to do.
		return true
	}

	if len(a) != len(b) {
		// Inconsistent number of domains.
		return false
	}

	// Assuming both are advertising DNSSL, the options must be identical.
	for i := range a {
		if len(a[i].DomainNames) != len(b[i].DomainNames) {
			return false
		}

		for j := range a[i].DomainNames {
			if a[i].DomainNames[j] != b[i].DomainNames[j] {
				return false
			}
		}

		if a[i].Lifetime != b[i].Lifetime {
			return false
		}
	}

	return true
}

// pickMTU selects a ndp.MTU option from the input options, reporting whether
// one was found.
func pickMTU(options []ndp.Option) (ndp.MTU, bool) {
	for _, o := range options {
		if m, ok := o.(*ndp.MTU); ok {
			return *m, true
		}
	}

	return 0, false
}

// pickPrefixes selects all ndp.PrefixInformation options from the input options.
func pickPrefixes(options []ndp.Option) []*ndp.PrefixInformation {
	var prefixes []*ndp.PrefixInformation
	for _, o := range options {
		if p, ok := o.(*ndp.PrefixInformation); ok {
			prefixes = append(prefixes, p)
		}
	}

	return prefixes
}

// pickRoutes selects all ndp.RouteInformation options from the input options.
func pickRoutes(options []ndp.Option) []*ndp.RouteInformation {
	var routes []*ndp.RouteInformation
	for _, o := range options {
		if r, ok := o.(*ndp.RouteInformation); ok {
			routes = append(routes, r)
		}
	}

	return routes
}

// pickRDNSS selects all ndp.RDNSS options from the input options.
func pickRDNSS(options []ndp.Option) []*ndp.RecursiveDNSServer {
	var rdnss []*ndp.RecursiveDNSServer
	for _, o := range options {
		if r, ok := o.(*ndp.RecursiveDNSServer); ok {
			rdnss = append(rdnss, r)
		}
	}

	return rdnss
}

// pickDNSSL selects all ndp.DNSSL options from the input options.
func pickDNSSL(options []ndp.Option) []*ndp.DNSSearchList {
	var dnssl []*ndp.DNSSearchList
	for _, o := range options {
		if d, ok := o.(*ndp.DNSSearchList); ok {
			dnssl = append(dnssl, d)
		}
	}

	return dnssl
}

// sourceLLA returns either the string for a source link-layer address or "unknown".
func sourceLLA(options []ndp.Option) string {
	for _, o := range options {
		if lla, ok := o.(*ndp.LinkLayerAddress); ok && lla.Direction == ndp.Source {
			return lla.Addr.String()
		}
	}

	return "unknown"
}
