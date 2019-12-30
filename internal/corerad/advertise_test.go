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

package corerad

import (
	"math/rand"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func Test_multicastDelay(t *testing.T) {
	// Static seed for deterministic output.
	r := rand.New(rand.NewSource(0))

	tests := []struct {
		name            string
		i               int
		min, max, delay time.Duration
	}{
		{
			name:  "static",
			min:   1 * time.Second,
			max:   1 * time.Second,
			delay: 1 * time.Second,
		},
		{
			name:  "random",
			min:   1 * time.Second,
			max:   10 * time.Second,
			delay: 4 * time.Second,
		},
		{
			name: "clamped",
			// Delay too long for low i value.
			i:     1,
			min:   30 * time.Second,
			max:   60 * time.Second,
			delay: maxInitialAdvInterval,
		},
		{
			name: "not clamped",
			// Delay appropriate for high i value.
			i:     100,
			min:   30 * time.Second,
			max:   60 * time.Second,
			delay: 52 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := multicastDelay(r, tt.i, tt.min.Nanoseconds(), tt.max.Nanoseconds())
			if diff := cmp.Diff(tt.delay, d); diff != "" {
				t.Fatalf("unexpected delay (-want +got):\n%s", diff)
			}
		})
	}
}
