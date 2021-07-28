// Copyright 2020-2021 Matt Layher
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
	"fmt"
	"net"
	"reflect"
	"strings"
	"time"

	"github.com/mdlayher/ndp"
)

// problems is a slice of problems with helper methods.
type problems []problem

// push adds a problem with the input data.
func (ps *problems) push(field, details string, want, got interface{}) {
	*ps = append(*ps, *newProblem(field, details, want, got))
}

// merge merges another problems slice with this one.
func (ps *problems) merge(pss problems) {
	*ps = append(*ps, pss...)
}

// A problem is an inconsistency detected in another router's RA.
type problem struct {
	Field, Details, Message string
}

// newProblem constructs a problem with the input fields.
func newProblem(field, details string, want, got interface{}) *problem {
	// Sanity check: any code using this API must pass identical types for
	// any sort of sane output.
	if reflect.TypeOf(want) != reflect.TypeOf(got) {
		panicf("corerad: newProblem types must match: %T != %T", want, got)
	}

	// If want and got are strings or can be stringified, we quote them for
	// easier reading.
	ws, okW := want.(string)
	gs, okG := got.(string)
	if okW && okG {
		return &problem{
			Field:   field,
			Details: details,
			Message: fmt.Sprintf("want: %q, got: %q", ws, gs),
		}
	}

	wStr, okW := want.(fmt.Stringer)
	gStr, okG := got.(fmt.Stringer)
	if okW && okG {
		return &problem{
			Field:   field,
			Details: details,
			Message: fmt.Sprintf("want: %q, got: %q", wStr.String(), gStr.String()),
		}
	}

	// Fall back to normal formatting.
	return &problem{
		Field:   field,
		Details: details,
		Message: fmt.Sprintf("want: %v, got: %v", want, got),
	}
}

// verifyRAs checks for consistency between two router advertisements.
func verifyRAs(a, b *ndp.RouterAdvertisement) []problem {
	// Verify the RA and its options using the rules established in:
	// https://tools.ietf.org/html/rfc4861#section-6.2.7.

	ps := checkRAs(a, b)
	ps.merge(checkMTUs(a.Options, b.Options))
	ps.merge(checkPrefixes(a.Options, b.Options))
	ps.merge(checkRoutes(a.Options, b.Options))
	ps.merge(checkRDNSS(a.Options, b.Options))
	ps.merge(checkDNSSL(a.Options, b.Options))

	return ps
}

// raConsistent verifies the base non-option fields of a and b for consistency.
func checkRAs(a, b *ndp.RouterAdvertisement) problems {
	var ps problems
	if a.CurrentHopLimit != b.CurrentHopLimit {
		ps.push("hop_limit", "", a.CurrentHopLimit, b.CurrentHopLimit)
	}

	if a.ManagedConfiguration != b.ManagedConfiguration {
		ps.push("managed_configuration", "", a.ManagedConfiguration, b.ManagedConfiguration)
	}

	if a.OtherConfiguration != b.OtherConfiguration {
		ps.push("other_configuration", "", a.OtherConfiguration, b.OtherConfiguration)
	}

	if !durationsConsistent(a.ReachableTime, b.ReachableTime) {
		ps.push("reachable_time", "", a.ReachableTime, b.ReachableTime)
	}

	if !durationsConsistent(a.RetransmitTimer, b.RetransmitTimer) {
		ps.push("retransmit_timer", "", a.RetransmitTimer, b.RetransmitTimer)
	}

	return ps
}

// durationsConsistent reports whether two time.Duration values are consistent.
func durationsConsistent(want, got time.Duration) bool {
	if want == 0 || got == 0 {
		// If either duration is unspecified, nothing to do.
		return true
	}

	return want == got
}

// checkMTUs reports whether two NDP MTU option values are consistent, or
// returns non-empty problems if not.
func checkMTUs(want, got []ndp.Option) problems {
	mtuA, okA := pickMTU(want)
	mtuB, okB := pickMTU(got)

	if !okA || !okB {
		// If either are not advertising MTUs, nothing to do.
		return nil
	}

	if mtuA == mtuB {
		return nil
	}

	var ps problems
	ps.push("mtu", "", mtuA, mtuB)
	return ps
}

// checkPrefixes reports whether two NDP PrefixInformation option values
// are consistent, or returns non-empty problems if not.
func checkPrefixes(want, got []ndp.Option) problems {
	pfxA := pickPrefixes(want)
	pfxB := pickPrefixes(got)

	if len(pfxA) == 0 || len(pfxB) == 0 {
		// If either are advertising no prefixes, nothing to do.
		return nil
	}

	var ps problems
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
			if a.PreferredLifetime != b.PreferredLifetime {
				ps.push("prefix_information_preferred_lifetime", prefixStr(a), a.PreferredLifetime, b.PreferredLifetime)
			}
			if a.ValidLifetime != b.ValidLifetime {
				ps.push("prefix_information_valid_lifetime", prefixStr(a), a.ValidLifetime, b.ValidLifetime)
			}
		}
	}

	return ps
}

// checkRoutes reports whether two NDP Route Information option values are
// consistent, or returns non-empty problems if not.
func checkRoutes(want, got []ndp.Option) problems {
	pfxA := pickRoutes(want)
	pfxB := pickRoutes(got)

	if len(pfxA) == 0 || len(pfxB) == 0 {
		// If either are advertising no routes, nothing to do.
		return nil
	}

	var ps problems
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
				ps.push("route_information_lifetime", routeStr(a), a.RouteLifetime, b.RouteLifetime)
			}
		}
	}

	return ps
}

// checkRDNSS reports whether two NDP Recursive DNS Servers option values are
// consistent, or returns non-empty problems if not.
func checkRDNSS(want, got []ndp.Option) problems {
	a := pickRDNSS(want)
	b := pickRDNSS(got)

	if len(a) == 0 || len(b) == 0 {
		// If either are advertising no RDNSS, nothing to do.
		return nil
	}

	var ps problems
	if len(a) != len(b) {
		// Inconsistent number of options, so we can perform no further checks.
		ps.push("rdnss_count", "", len(a), len(b))
		return ps
	}

	// Assuming both are advertising RDNSS, the options must be identical.
	for i := range a {
		if a, b := a[i].Lifetime, b[i].Lifetime; a != b {
			ps.push("rdnss_lifetime", "", a, b)
		}

		if len(a[i].Servers) != len(b[i].Servers) {
			// Inconsistent number of servers, so we can perform no further checks.
			ps.push("rdnss_servers", "", ipsStr(a[i].Servers), ipsStr(b[i].Servers))
			continue
		}

		equal := true
		for j := range a[i].Servers {
			if a, b := a[i].Servers[j], b[i].Servers[j]; !a.Equal(b) {
				equal = false
				break

			}
		}
		if !equal {
			ps.push("rdnss_servers", "", ipsStr(a[i].Servers), ipsStr(b[i].Servers))
		}
	}

	return ps
}

// checkDNSSL reports whether two NDP DNS Search List option values are
// consistent, or returns non-empty problems if not.
func checkDNSSL(want, got []ndp.Option) problems {
	a := pickDNSSL(want)
	b := pickDNSSL(got)

	if len(a) == 0 || len(b) == 0 {
		// If either are advertising no DNSSL, nothing to do.
		return nil
	}

	var ps problems
	if len(a) != len(b) {
		// Inconsistent number of domains, so we can perform no further checks.
		ps.push("dnssl_count", "", len(a), len(b))
		return ps
	}

	join := func(ss []string) string {
		return strings.Join(ss, ", ")
	}

	// Assuming both are advertising DNSSL, the options must be identical.
	for i := range a {
		if a, b := a[i].Lifetime, b[i].Lifetime; a != b {
			ps.push("dnssl_lifetime", "", a, b)
		}

		if len(a[i].DomainNames) != len(b[i].DomainNames) {
			ps.push("dnssl_domain_names", "", join(a[i].DomainNames), join(b[i].DomainNames))
			// Inconsistent number of domain names, so we can perform no
			// further checks.
			continue
		}

		equal := true
		for j := range a[i].DomainNames {
			if a, b := a[i].DomainNames[j], b[i].DomainNames[j]; a != b {
				equal = false
				break
			}
		}
		if !equal {
			ps.push("dnssl_domain_names", "", join(a[i].DomainNames), join(b[i].DomainNames))
		}
	}

	return ps
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

func ipsStr(ips []net.IP) string {
	ss := make([]string, 0, len(ips))
	for _, ip := range ips {
		ss = append(ss, ip.String())
	}

	return strings.Join(ss, ", ")
}
