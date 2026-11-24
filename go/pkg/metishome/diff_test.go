package metishome

// diffWorlds: where two worlds part, for a failing replay test.

import (
	"encoding/json"
	"fmt"
	"strings"

	semhome "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/home"
)

func diffWorlds(a, b *semhome.Home) string {
	var out []string
	ja, _ := json.MarshalIndent(a.W, "", " ")
	jb, _ := json.MarshalIndent(b.W, "", " ")
	la, lb := strings.Split(string(ja), "\n"), strings.Split(string(jb), "\n")
	for i := 0; i < len(la) && i < len(lb); i++ {
		if la[i] != lb[i] {
			lo := i - 6
			if lo < 0 {
				lo = 0
			}
			out = append(out, fmt.Sprintf("world line %d:\n live:  %s\n again: %s\n context: %s", i, la[i], lb[i], strings.Join(la[lo:i], "|")))
			break
		}
	}
	sa, _ := json.Marshal(a.B.State())
	sb, _ := json.Marshal(b.B.State())
	if string(sa) != string(sb) {
		out = append(out, "board:\n live:  "+string(sa)+"\n again: "+string(sb))
	}
	return strings.Join(out, "\n")
}
