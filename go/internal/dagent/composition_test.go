package dagent

import (
	"path/filepath"
	"reflect"
	"testing"
)

// TestTheKitchenPotImageIsRead: the shared-pot kitchen's image (metis-catalog kitchen_pot, built):
// its members in the tick's order, roles, tables, the protocol's report natures, the director.
func TestTheKitchenPotImageIsRead(t *testing.T) {
	c, err := ReadComposition(filepath.Join(catalogDir(t), "domains", "kitchen_pot"))
	if err != nil {
		t.Skip(err)
	}
	if c.Name != "kitchen_pot" || c.Direct() != "pot_director" || len(c.Hash) != 12 {
		t.Fatalf("%s %s %s", c.Name, c.Direct(), c.Hash)
	}
	if got := c.BeatOrder(); !reflect.DeepEqual(got, []string{"cook_a", "cook_b", "pot_director"}) {
		t.Fatal(got)
	}
	a := c.Member("cook_a")
	if a.Role != "specialist" || a.Gating != "program" || a.Shared["tick__want"] != "out" || a.Beats() != 1 ||
		!reflect.DeepEqual(a.Status["success"], []string{"tick__done"}) {
		t.Fatalf("%+v", a)
	}
	if c.Member("pot_director").Resources["pot"] == nil || c.Member("pot_director").Gating != "host" {
		t.Fatal("the director's resource, its default gating")
	}
	r := c.Reports()
	if !r["tick__want"] || !r["tick__done"] || r["tick__status"] || r["tick__grant"] {
		t.Fatal(r)
	}
	if c.Director.Name != "pot_director" || len(c.Director.Facts) != 2 || c.Director.Generated {
		t.Fatalf("%+v", c.Director)
	}
}
