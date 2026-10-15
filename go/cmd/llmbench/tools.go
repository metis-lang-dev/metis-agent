package main

// The REPL API's declarations (repl-tools step 3): the decider's and the
// v3 orchestrator's tools render from a bundle's tools.json (metispy's
// metis.lang.repl_api, written by the emitter), never from a copy here.

import (
	"flag"
	"fmt"

	"github.com/metis-lang-dev/metis-agent/go/internal/kernel"
	"github.com/metis-lang-dev/metis-agent/go/internal/repltools"
)

var toolsBundle = flag.String("tools-bundle", "../../metis-catalog/domains/dog/build/dog.metis",
	"the bundle whose tools.json (the REPL API) the decider and the v3 orchestrator render from "+
		"(emit it: python -m metis.kernel.artifact dog)")

var toolsRead []repltools.Tool

// replTools is the bundle's REPL API (read once).
func replTools() []repltools.Tool {
	if toolsRead == nil {
		t, err := repltools.Read(*toolsBundle)
		if err != nil {
			fail(fmt.Errorf("the REPL API: %v (emit the bundle: python -m metis.kernel.artifact dog, or -tools-bundle)", err))
		}
		toolsRead = t
	}
	return toolsRead
}

// newDecider is kernel.NewDecider on the bundle's REPL API.
func newDecider(cards []kernel.Card) *kernel.Decider {
	d, err := kernel.NewDecider(cards, replTools())
	if err != nil {
		fail(err)
	}
	return d
}
