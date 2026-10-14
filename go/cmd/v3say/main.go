// Command v3say speaks to a running metis-agent over /ws as the page does
// (say_text, intent mode) and prints what comes back — the intent turns and
// the narrations — for a while after each line (integration step 2's smoke).
//
//	v3say -addr localhost:8395 -wait 6s "go to the door" "what are you doing?"
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	addr := flag.String("addr", "localhost:8395", "the service")
	wait := flag.Duration("wait", 6*time.Second, "listen this long after each line")
	world := flag.String("world", "", `world lines for the dog's simulated world, e.g. "-closed(kitchen) +open(kitchen)"`)
	domain := flag.String("domain", "dog", "the domain -world acts on")
	flag.Parse()
	c, _, err := websocket.DefaultDialer.Dial("ws://"+*addr+"/ws", nil)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	go func() {
		for {
			_, b, err := c.ReadMessage()
			if err != nil {
				return
			}
			var m map[string]any
			if json.Unmarshal(b, &m) == nil {
				switch m["ev"] {
				case "intent", "narration", "error":
					fmt.Printf("%s  %s\n", time.Now().Format("15:04:05.0"), b)
				}
			}
		}
	}()
	if *world != "" {
		msg, _ := json.Marshal(map[string]any{"ev": "world", "text": *world, "mode": *domain})
		if err := c.WriteMessage(websocket.TextMessage, msg); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s  world %s: %s\n", time.Now().Format("15:04:05.0"), *domain, *world)
		time.Sleep(time.Second)
	}
	for i, line := range flag.Args() {
		fmt.Printf("%s  > %s\n", time.Now().Format("15:04:05.0"), line)
		msg, _ := json.Marshal(map[string]any{"ev": "say_text", "id": fmt.Sprintf("s%d", i+1), "text": line, "mode": "intent"})
		if err := c.WriteMessage(websocket.TextMessage, msg); err != nil {
			log.Fatal(err)
		}
		time.Sleep(*wait)
	}
}
