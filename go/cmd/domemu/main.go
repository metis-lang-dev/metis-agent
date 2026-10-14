// Command domemu runs emulated domain agents on stdin/stdout, the domain
// agent's JSON lines (internal/domemu; orchestrator-agent step 3).
//
//	domemu -scripts scripts.json < lines
package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"

	"github.com/metis-lang-dev/metis-agent/go/internal/domemu"
)

func main() {
	path := flag.String("scripts", "", "a JSON list of domain scripts (internal/domemu Script)")
	flag.Parse()
	b, err := os.ReadFile(*path)
	if err != nil {
		log.Fatal(err)
	}
	var scripts []domemu.Script
	if err := json.Unmarshal(b, &scripts); err != nil {
		log.Fatal(err)
	}
	if err := domemu.New(scripts).Serve(os.Stdin, os.Stdout); err != nil {
		log.Fatal(err)
	}
}
