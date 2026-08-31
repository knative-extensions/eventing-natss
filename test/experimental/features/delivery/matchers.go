/*
Copyright 2026 The Knative Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package delivery

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"knative.dev/reconciler-test/pkg/eventshub"
)

func eventWithKind(id string, kind eventshub.EventKind) eventshub.EventInfoMatcher {
	return func(info eventshub.EventInfo) error {
		if info.Event == nil {
			return fmt.Errorf("received event is nil")
		}
		if info.Event.ID() != id {
			return fmt.Errorf("received event ID %q, expected %q", info.Event.ID(), id)
		}
		if info.Kind != kind {
			return fmt.Errorf("received event kind %q, expected %q", info.Kind, kind)
		}
		return nil
	}
}

func deliveriesFollowIntervals(id string, expected []time.Duration) eventshub.EventInfoMatcher {
	type deliveryKey struct {
		kind     eventshub.EventKind
		sequence uint64
	}

	var mu sync.Mutex
	seen := make(map[deliveryKey]eventshub.EventInfo, len(expected)+1)

	return func(info eventshub.EventInfo) error {
		if info.Event == nil {
			return fmt.Errorf("received event is nil")
		}
		if info.Event.ID() != id {
			return fmt.Errorf("received event ID %q, expected %q", info.Event.ID(), id)
		}

		mu.Lock()
		defer mu.Unlock()
		seen[deliveryKey{kind: info.Kind, sequence: info.Sequence}] = info
		if len(seen) < len(expected)+1 {
			return nil
		}

		deliveries := make([]eventshub.EventInfo, 0, len(seen))
		for _, delivery := range seen {
			deliveries = append(deliveries, delivery)
		}
		sort.Slice(deliveries, func(i, j int) bool {
			return deliveries[i].Time.Before(deliveries[j].Time)
		})

		for i, wait := range expected {
			actual := deliveries[i+1].Time.Sub(deliveries[i].Time)
			if actual < wait-500*time.Millisecond || actual > wait+3*time.Second {
				return fmt.Errorf("delivery %d waited %s, expected %s", i+2, actual, wait)
			}
		}
		return nil
	}
}
