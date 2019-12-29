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
	"errors"
	"fmt"
	"time"
)

// parseInterfaces parses a rawInterface into an Interface.
func parseInterface(ifi rawInterface) (*Interface, error) {
	// Default values in this section  come from the RFC:
	// https://tools.ietf.org/html/rfc4861#section-6.2.1.

	maxInterval := 600 * time.Second
	if ifi.MaxInterval != "" {
		d, err := time.ParseDuration(ifi.MaxInterval)
		if err != nil {
			return nil, fmt.Errorf("invalid max interval: %v", err)
		}
		maxInterval = d
	}

	if maxInterval < 4*time.Second || maxInterval > 1800*time.Second {
		return nil, errors.New("max interval must be between 4 and 1800 seconds")
	}

	minInterval, err := parseMinInterval(ifi.MinInterval, maxInterval)
	if err != nil {
		return nil, err
	}

	return &Interface{
		Name:               ifi.Name,
		SendAdvertisements: ifi.SendAdvertisements,
		MinInterval:        minInterval,
		MaxInterval:        maxInterval,
	}, nil
}

// parseMinInterval parses a min_interval string and computes its value
// based on user input or the relationship with max.
func parseMinInterval(minRaw string, max time.Duration) (time.Duration, error) {
	var min time.Duration
	if minRaw == "" || minRaw == "auto" {
		// Compute a sane default per the RFC.
		if max >= 9*time.Second {
			min = time.Duration(0.33 * float64(max)).Truncate(time.Second)
		} else {
			min = max
		}

		return min, nil
	}

	// Use the user's value, but validate it per the RFC.
	d, err := time.ParseDuration(minRaw)
	if err != nil {
		return 0, fmt.Errorf("invalid min interval: %v", err)
	}
	min = d

	upper := time.Duration(0.75 * float64(max)).Truncate(time.Second)
	if min < 3*time.Second || min > upper {
		return 0, fmt.Errorf("min interval must be between 3 and (.75 * max_interval = %d) seconds", int(upper.Seconds()))
	}

	return min, nil
}
