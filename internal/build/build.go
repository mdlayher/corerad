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

// Package build provides metadata about the current build of CoreRAD.
package build

import (
	"fmt"
	"strconv"
	"time"
)

var (
	// Variables populated by linker flags.
	linkTimestamp string
	linkVersion   string

	// timeT is the time when CoreRAD was built, or zero time if none was
	// specified at link-time.
	timeT = func() time.Time {
		if linkTimestamp == "" {
			return time.Time{}
		}

		s, err := strconv.ParseInt(linkTimestamp, 10, 64)
		if err != nil {
			panicf("failed to parse raw UNIX timestamp string: %v", err)
		}

		return time.Unix(s, 0)
	}()
)

// Banner produces a string banner containing metadata about the currently
// running CoreRAD binary.
func Banner() string {
	// Use n/a as a placeholder if no time set.
	tstr := "n/a"
	if t := Time(); !t.IsZero() {
		tstr = t.Format("2006-01-02")
	}

	return fmt.Sprintf("CoreRAD %s (%s)",
		Version(),
		tstr,
	)
}

// Time produces a time.Time value for when CoreRAD was built, or zero time if
// none was specified at link-time.
func Time() time.Time { return timeT }

// Version produces a Version string or "development" if none was specified
// at link-time.
func Version() string {
	if linkVersion == "" {
		return "development"
	}

	return linkVersion
}

func panicf(format string, a ...interface{}) {
	panic(fmt.Sprintf(format, a...))
}
