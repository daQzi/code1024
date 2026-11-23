// SPDX-License-Identifier: AGPL-3.0-or-later
// Time probe names and UTC fallback follow QuantumNous/new-api billingexpr/run.go
// at 48f9df96ea7c68eae04a0f3687869f232f48929f. Durable freezing is host-specific.
package taskprice

import (
	"errors"
	"slices"
	"strings"
	"time"
)

// ClockSnapshot contains only the admission instant and offsets of literal
// zones actually used by this expression. Offsets also pin DST/tzdata behavior
// across restarts, different host images and timezone-database updates.
type ClockSnapshot struct {
	At      time.Time      `json:"at"`
	Offsets map[string]int `json:"offsets"`
}

func (p *Program) CaptureClock(at time.Time) (*ClockSnapshot, error) {
	if len(p.zones) == 0 {
		return nil, nil
	}
	if at.IsZero() {
		return nil, errors.New("task price time probes require an admission instant")
	}
	clock := &ClockSnapshot{At: at.UTC(), Offsets: make(map[string]int, len(p.zones))}
	for zone, location := range p.zones {
		_, clock.Offsets[zone] = at.In(location).Zone()
	}
	return clock, nil
}

func (p *Program) validateClock(clock *ClockSnapshot) error {
	if len(p.zones) == 0 {
		return nil
	}
	if clock == nil || clock.At.IsZero() || len(clock.Offsets) != len(p.zones) {
		return errors.New("task price time snapshot is missing or invalid")
	}
	for zone := range p.zones {
		offset, ok := clock.Offsets[zone]
		if !ok || offset < -24*60*60 || offset > 24*60*60 {
			return errors.New("task price time snapshot has an invalid zone offset")
		}
	}
	return nil
}

func (c *ClockSnapshot) inZone(zone string) time.Time {
	if c == nil {
		return time.Time{} // Compile-time type prototype; never used to settle.
	}
	zone = strings.TrimSpace(zone)
	return c.At.In(time.FixedZone(zone, c.Offsets[zone]))
}

// Calendar checks supplement the existing 64 usage vectors, rather than
// displacing their coverage. At most 64 instants are checked against the first
// and last usage vectors. Sampling never replaces validation of the actual run.
func (p *Program) schemaClocks() []*ClockSnapshot {
	zones := make([]string, 0, len(p.zones))
	for zone := range p.zones {
		zones = append(zones, zone)
	}
	slices.Sort(zones)
	var samples []*ClockSnapshot
	seen := make(map[time.Time]bool)
	for _, zone := range zones {
		location := p.zones[zone]
		var instants []time.Time
		for day := range 7 {
			instants = append(instants, time.Date(2000, time.January, 2+day, 12, 30, 0, 0, location))
		}
		instants = append(instants, time.Date(2000, time.January, 1, 0, 0, 0, 0, location), time.Date(2000, time.February, 29, 1, 1, 0, 0, location), time.Date(2000, time.December, 31, 23, 59, 0, 0, location))
		for _, at := range instants {
			at = at.UTC()
			if seen[at] {
				continue
			}
			seen[at] = true
			clock, _ := p.CaptureClock(at)
			samples = append(samples, clock)
			if len(samples) == 64 {
				return samples
			}
		}
	}
	return samples
}
