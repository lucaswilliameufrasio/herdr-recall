package web

import (
	"fmt"
	"time"
)

// Machine represents the non-secret information shown in the machine list.
type Machine struct {
	ID        string
	Name      string
	Freshness string
	Boot      string
	PaneCount int
}

type Boot struct {
	ID            string
	FirstReceived string
}

type PresenceSlot struct {
	X     int
	Class string
}

type PresenceChart struct {
	Label string
	Slots []PresenceSlot
}

type Detail struct {
	ID          string
	Name        string
	Freshness   string
	Boot        string
	Boots       []Boot
	HasSnapshot bool
	Panes       []DeskPane
	Presence    PresenceChart
}

// Chart anchors the window on the observed timestamp of the latest received
// presence. Entries are bucketed by observed time, not by receipt time.
func Chart(anchor time.Time, observed []time.Time) PresenceChart {
	if anchor.IsZero() {
		return PresenceChart{}
	}
	anchor = anchor.UTC().Truncate(time.Minute)
	buckets := make(map[time.Time]bool, len(observed))
	for _, at := range observed {
		buckets[at.UTC().Truncate(time.Minute)] = true
	}
	slots := make([]PresenceSlot, 0, 15)
	count := 0
	for i := 0; i < 15; i++ {
		class := "miss"
		if buckets[anchor.Add(time.Duration(i-14)*time.Minute)] {
			class = "beat"
			count++
		}
		slots = append(slots, PresenceSlot{X: i * 10, Class: class})
	}
	return PresenceChart{
		Label: fmt.Sprintf("Presença em 15 minutos até %s UTC: %d de 15.", anchor.Format("2006-01-02 15:04"), count),
		Slots: slots,
	}
}
