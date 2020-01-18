// Copyright 2019 Matt Layher
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

package config

import (
	"fmt"
	"time"
)

// parseInterfaces parses a rawInterface into an Interface.
func parseInterface(ifi rawInterface) (*Interface, error) {
	// The RFC and radvd have different defaults for some values. Where they
	// differ, we will mimic radvd's defaults as many users will likely be
	// migrating their configurations directly from radvd:
	// https://linux.die.net/man/5/radvd.conf.

	maxInterval := 600 * time.Second
	if ifi.MaxInterval != "" {
		d, err := time.ParseDuration(ifi.MaxInterval)
		if err != nil {
			return nil, fmt.Errorf("invalid max interval: %v", err)
		}
		maxInterval = d
	}

	if maxInterval < 4*time.Second || maxInterval > 1800*time.Second {
		return nil, fmt.Errorf("max interval (%d) must be between 4 and 1800 seconds", int(maxInterval.Seconds()))
	}

	minInterval, err := parseMinInterval(ifi.MinInterval, maxInterval)
	if err != nil {
		return nil, err
	}

	var reachable time.Duration
	if ifi.ReachableTime != "" {
		d, err := time.ParseDuration(ifi.ReachableTime)
		if err != nil {
			return nil, fmt.Errorf("invalid reachable time: %v", err)
		}
		reachable = d
	}

	if reachable < 0*time.Second || reachable > 1*time.Hour {
		return nil, fmt.Errorf("reachable time (%d) must be between 0 and 3600 seconds", int(reachable.Seconds()))
	}

	var retrans time.Duration
	if ifi.RetransmitTimer != "" {
		d, err := time.ParseDuration(ifi.RetransmitTimer)
		if err != nil {
			return nil, fmt.Errorf("invalid retransmit timer: %v", err)
		}
		retrans = d
	}

	// TODO: is this upper bound right?
	if retrans < 0*time.Second || retrans > 1*time.Hour {
		return nil, fmt.Errorf("retransmit timer (%d) must be between 0 and 3600 seconds", int(retrans.Seconds()))
	}

	hopLimit := 64
	if ifi.HopLimit != nil {
		// Override if specified.
		hopLimit = *ifi.HopLimit
	}

	if hopLimit < 0 || hopLimit > 255 {
		return nil, fmt.Errorf("hop limit (%d) must be between 0 and 255", hopLimit)
	}

	lifetime, err := parseDefaultLifetime(ifi.DefaultLifetime, maxInterval)
	if err != nil {
		return nil, err
	}

	// Parse plugins using the remaining rawInterface fields.
	plugins, err := parsePlugins(ifi, maxInterval)
	if err != nil {
		return nil, err
	}

	return &Interface{
		Name:               ifi.Name,
		SendAdvertisements: ifi.SendAdvertisements,
		MinInterval:        minInterval,
		MaxInterval:        maxInterval,
		Managed:            ifi.Managed,
		OtherConfig:        ifi.OtherConfig,
		ReachableTime:      reachable,
		RetransmitTimer:    retrans,
		HopLimit:           uint8(hopLimit),
		DefaultLifetime:    lifetime,
		UnicastOnly:        ifi.UnicastOnly,
		Plugins:            plugins,
	}, nil
}

// parseMinInterval parses a min_interval string and computes its value
// based on user input or the relationship with max.
func parseMinInterval(s string, max time.Duration) (time.Duration, error) {
	if s == "" || s == "auto" {
		// Compute a sane default per the RFC.
		if max >= 9*time.Second {
			return time.Duration(0.33 * float64(max)).Truncate(time.Second), nil
		}

		// For lower values, use the same value as max.
		return max, nil
	}

	// Use the user's value, but validate it per the RFC.
	min, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid min interval: %v", err)
	}

	upper := time.Duration(0.75 * float64(max)).Truncate(time.Second)
	if min < 3*time.Second || min > upper {
		return 0, fmt.Errorf("min interval (%d) must be between 3 and (.75 * max_interval = %d) seconds",
			int(min.Seconds()), int(upper.Seconds()))
	}

	return min, nil
}

// parseDefaultLifetime parses a default_lifetime string and computes its value
// based on user input or the relationship with max.
func parseDefaultLifetime(s *string, max time.Duration) (time.Duration, error) {
	auto := 3 * max

	if s == nil {
		return auto, nil
	}

	switch *s {
	case "":
		// No value specified, no default lifetime.
		return 0, nil
	case "auto":
		// Compute a sane default per the RFC.
		return auto, nil
	}

	// Use the user's value, but validate it per the RFC.
	lt, err := time.ParseDuration(*s)
	if err != nil {
		return 0, fmt.Errorf("invalid default lifetime: %v", err)
	}

	if lt != 0 && (lt < max || lt > 9000*time.Second) {
		return 0, fmt.Errorf("default lifetime (%d) must be 0 or between (max_interval = %d) and 9000 seconds", int(lt.Seconds()), int(max.Seconds()))
	}

	return lt, nil
}
