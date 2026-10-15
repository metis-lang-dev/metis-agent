package host

import (
	"encoding/json"
	"fmt"
)

// The plant (metis-catalog domains/plant/plant_host.py): the sun dries the
// soil every SunEvery beats while it is moist; VisitorAfter dry beats on a
// visitor waters it — reported as +visitor a beat before (the pack's
// trigger), off-model otherwise: the surprise.
const (
	PlantSunEvery     = 6
	PlantVisitorAfter = 8
	PlantLast         = 30
)

// PlantTables are plant_host.py's.
var PlantTables = Tables{
	Outcomes: []string{"done", "checked"},
	Pairs:    [][2]string{{"dry", "moist"}},
	Words:    map[string]string{"moist": "wait until my soil dries out"},
	Keys:     map[string]string{"moist": "soil"},
}

// PlantScript is the plant's hosted day (beat -> do-lines).
func PlantScript() map[int][]string {
	return map[int][]string{
		2: {"+cmd_check"}, 8: {"+cmd_check"}, 9: {"+cmd_water"},
		16: {"+cmd_check"}, 26: {"+cmd_check"},
	}
}

// Plant is the plant's host.
type Plant struct {
	day
	moist    bool
	dryBeats int
	visits   int
}

// NewPlant is the plant on its hosted day.
func NewPlant() *Plant {
	return &Plant{day: day{Script: PlantScript(), Last: PlantLast, tables: &PlantTables}, moist: true}
}

func (p *Plant) Tick(counts map[string]int) ([]string, bool) {
	dos, ok := p.begin(counts)
	if !ok {
		return nil, true
	}
	if p.moist && p.beat%PlantSunEvery == 0 {
		dos = append(dos, "+sun")
	}
	if !p.moist { // the visitor: off-model
		p.dryBeats++
		if p.dryBeats == PlantVisitorAfter-1 {
			dos = append(dos, "+visitor")
		} else if p.dryBeats >= PlantVisitorAfter {
			p.moist, p.dryBeats, p.visits = true, 0, p.visits+1
		}
	}
	return append(dos, p.Script[p.beat]...), false
}

func (p *Plant) OnEvent(name string, args []string) {
	switch p.actuate(name, args) {
	case "dry_out":
		p.moist, p.dryBeats = false, 0
	case "water", "visitor_waters":
		p.moist, p.dryBeats = true, 0
	}
}

func (p *Plant) Observe(map[string]int) map[string]int {
	if p.moist {
		return map[string]int{"moist": 1, "dry": 0}
	}
	return map[string]int{"moist": 0, "dry": 1}
}

// Snapshot is [beat, due, acted, moist, dry_beats, visits] (py's tuple).
func (p *Plant) Snapshot() (json.RawMessage, error) {
	return json.Marshal([]any{p.beat, nonNil(p.due), nonNil(p.acted), p.moist, p.dryBeats, p.visits})
}

func (p *Plant) Restore(b json.RawMessage) error {
	var x []json.RawMessage
	if err := json.Unmarshal(b, &x); err != nil || len(x) != 6 {
		return fmt.Errorf("plant snapshot: want 6 fields, got %s", b)
	}
	for i, dst := range []any{&p.beat, &p.due, &p.acted, &p.moist, &p.dryBeats, &p.visits} {
		if err := json.Unmarshal(x[i], dst); err != nil {
			return fmt.Errorf("plant snapshot field %d: %w", i, err)
		}
	}
	return nil
}
