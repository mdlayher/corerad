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

//+build linux

package corerad

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/sync/errgroup"
)

func TestAdvertiserLinuxIPv6Autoconfiguration(t *testing.T) {
	ad, _, _, done := testAdvertiser(t, nil)
	defer done()

	// Capture the IPv6 autoconfiguration state while the advertiser is running
	// and immediately after it stops.
	start, err := getIPv6Autoconf(ad.ifi.Name)
	if err != nil {
		t.Fatalf("failed to get start state: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var eg errgroup.Group
	eg.Go(func() error {
		if err := ad.Advertise(ctx); err != nil {
			return fmt.Errorf("failed to advertise: %v", err)
		}

		return nil
	})

	cancel()
	if err := eg.Wait(); err != nil {
		t.Fatalf("failed to stop advertiser: %v", err)
	}

	end, err := getIPv6Autoconf(ad.ifi.Name)
	if err != nil {
		t.Fatalf("failed to get end state: %v", err)
	}

	// Expect the advertiser to disable IPv6 autoconfiguration and re-enable
	// it once it's done.
	if diff := cmp.Diff([]bool{false, true}, []bool{start, end}); diff != "" {
		t.Fatalf("unexpected IPv6 autoconfiguration states (-want +got):\n%s", diff)
	}

}
