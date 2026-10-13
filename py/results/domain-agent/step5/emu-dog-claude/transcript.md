# orchestrator emulator — dog_host.py's SCRIPT as intents: the person's command of each beat, typed as the executive types it (go/grammar/dog.gbnf); the world's changes and answers stay the host's. After the day, the REPL API reads of dog-transcript.md.

── ready; beat 1 (intents sent now are asserted at its tick)
→ `{"id": "c1", "domain": "dog", "verb": "LOCAL_ACTION", "args": {"action": "SIT_DOWN"}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 0}, "text": "Accepted.", "id": "c1", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 2, "beat": 1, "event": "sit[P=standing]"}, "text": "I sit down.", "id": "c1", "status": "done"}`
── beat 2
→ `{"id": "c2", "domain": "dog", "verb": "NAVIGATE", "args": {"goal": {"type": "REGION", "description": "kitchen"}}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 1}, "text": "Accepted.", "id": "c2", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 4, "beat": 2, "event": "go_closed[P=kitchen]"}, "text": "I can't go there: the way to it is closed.", "id": "c2", "status": "refused", "reason": "the way to it is closed", "alternatives": ["door", "tag3"], "blocker": "closed(kitchen)"}`
── beat 3
→ `{"id": "c3", "domain": "dog", "verb": "LOCAL_ACTION", "args": {"action": "STAND_UP"}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 2}, "text": "Accepted.", "id": "c3", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 6, "beat": 3, "event": "stand[P=sitting]"}, "text": "I stand up.", "id": "c3", "status": "done"}`
── beat 4
→ `{"id": "c4", "domain": "dog", "verb": "NAVIGATE", "args": {"goal": {"type": "TAG", "description": "april tag"}, "tag_id": 3}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 3}, "text": "Accepted.", "id": "c4", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 8, "beat": 4, "event": "go[P=tag3]"}, "text": "I set off for the place you named.", "id": "c4", "status": "progress"}`
── beat 5
→ `{"id": "c5", "domain": "dog", "verb": "FIND", "args": {"target": {"type": "OBJECT", "description": "keys"}}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 4}, "text": "Accepted.", "id": "c5", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 10, "beat": 5, "event": "find_busy[T=keys,P=tag3]"}, "text": "I can't take that now: I am already busy with a task.", "id": "c5", "status": "refused", "reason": "I am already busy with a task", "alternatives": ["cancel"], "blocker": null}`
── beat 6
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 12, "beat": 6, "event": "arrive[P=tag3,Q=door]"}, "text": "I have arrived.", "id": "c4", "status": "done"}`
── beat 7
→ `{"id": "c6", "domain": "dog", "verb": "LOCAL_ACTION", "args": {"action": "MOVE", "direction": "BACKWARD"}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 6}, "text": "Accepted.", "id": "c6", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 14, "beat": 7, "event": "move_wall[W=backward]"}, "text": "I can't move that way: there is a wall right there.", "id": "c6", "status": "refused", "reason": "there is a wall right there", "alternatives": [], "blocker": "wall(backward)"}`
── beat 8
→ `{"id": "o1", "domain": "dog", "tool": "consult", "args": {"question": "Why there is a wall right there?", "max_calls": 6}}`
    ← `{"domain": "dog", "kind": "consult", "ref": {"seq": 14, "beat": 7}, "text": "The wall was there from the start: wall(backward) is part of the base state, and no event produced it. When the dog was told to move backward, event 14 move_wall[W=backward] refused the move because the dog was standing (posture(standing)) and there was a wall behind it. Moving the dog away from the wall, or asking it to move a clear way such as forward, would let it move.", "id": "o1", "answer_kind": "missing", "resource": {"atom": "wall(backward)", "words": "a wall right behind the dog"}, "evidence": ["posture(standing) 1", "wall(backward) 1"], "calls": 0}`
→ `{"id": "c7", "domain": "dog", "verb": "FIND", "args": {"target": {"type": "OBJECT", "description": "fire extinguisher"}}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 7}, "text": "Accepted.", "id": "c7", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 16, "beat": 8, "event": "find[T=extinguisher]"}, "text": "I start looking for it.", "id": "c7", "status": "progress"}`
── beat 9
→ `{"id": "c8", "domain": "dog", "verb": "QUERY", "args": {"target": {"type": "OBJECT", "description": "chair"}}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 8}, "text": "Accepted.", "id": "c8", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 18, "beat": 9, "event": "query_seen[T=chair,P=kitchen]"}, "text": "I tell you where it is: I have seen it at that place.", "id": "c8", "status": "done"}`
── beat 10
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 20, "beat": 10, "event": "find_locked[T=extinguisher]"}, "text": "I can't reach it: it is locked away.", "id": "c7", "status": "refused", "reason": "it is locked away", "alternatives": [], "blocker": "locked_away(extinguisher)"}`
── beat 11
→ `{"id": "o2", "domain": "dog", "tool": "consult", "args": {"question": "Why it is locked away?", "max_calls": 6}}`
    ← `{"domain": "dog", "kind": "consult", "ref": {"seq": 20, "beat": 10}, "text": "Nothing the dog did locked the extinguisher. The fact locked_away(extinguisher) came from outside the dog's rules, entered as a world fact at step 19 while the dog was searching for it. At event 20 find_locked[T=extinguisher] used that fact, so the dog stopped searching and refused find. A person would have to unlock or free the extinguisher before the dog could reach it.", "id": "o2", "answer_kind": "missing", "resource": {"atom": "locked_away(extinguisher)", "words": "the extinguisher is locked away and needs to be unlocked"}, "calls": 1}`
→ `{"id": "c9", "domain": "dog", "verb": "FOLLOW", "args": {"target": {"type": "PERSON", "description": "the person"}}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 10}, "text": "Accepted.", "id": "c9", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 22, "beat": 11, "event": "follow"}, "text": "I follow you.", "id": "c9", "status": "progress"}`
── beat 12
→ `{"id": "c10", "domain": "dog", "verb": "STOP", "args": {}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 11}, "text": "Accepted.", "id": "c10", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 24, "beat": 12, "event": "stop_follow"}, "text": "I stop following you.", "id": "c9", "status": "cancelled", "reason": "I stop following you"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 24, "beat": 12, "event": "stop_follow"}, "text": "I stop following you.", "id": "c10", "status": "done"}`
── beat 13
→ `{"id": "c11", "domain": "dog", "verb": "UNKNOWN", "args": {}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 12}, "text": "Accepted.", "id": "c11", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 26, "beat": 13, "event": "unknown"}, "text": "I can't do that: it is not something I can or may do.", "id": "c11", "status": "refused", "reason": "it is not something I can or may do", "alternatives": [], "blocker": null}`
── beat 14
→ `{"id": "c12", "domain": "dog", "verb": "LOCAL_ACTION", "args": {"action": "SHAKE"}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 13}, "text": "Accepted.", "id": "c12", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 28, "beat": 14, "event": "paw"}, "text": "I give you my paw.", "id": "c12", "status": "done"}`
── beat 15
→ `{"id": "c13", "domain": "dog", "verb": "FIND", "args": {"target": {"type": "OBJECT", "description": "bottle", "attributes": {"color": "blue"}}}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 14}, "text": "Accepted.", "id": "c13", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 30, "beat": 15, "event": "find_low[T=bottle]"}, "text": "I can't search: the battery is too low to search.", "id": "c13", "status": "refused", "reason": "the battery is too low to search", "alternatives": [], "blocker": "battery_low"}`
── beat 16
→ `{"id": "o3", "domain": "dog", "tool": "consult", "args": {"question": "Why the battery is too low to search?", "max_calls": 6}}`
    ← `{"domain": "dog", "kind": "consult", "ref": {"seq": 30, "beat": 15}, "text": "The search for the bottle was refused at event 30 by find_low[T=bottle] because the battery is low: battery_low holds, and it was last produced by event 29. Charging the dog's battery would let it accept the find command.", "id": "o3", "answer_kind": "missing", "resource": {"atom": "battery_low", "words": "a charged battery"}, "evidence": ["battery_low 1"], "calls": 0}`
→ `{"id": "c14", "domain": "dog", "verb": "LOCAL_ACTION", "args": {"action": "SIT_DOWN"}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 15}, "text": "Accepted.", "id": "c14", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 32, "beat": 16, "event": "sit[P=standing]"}, "text": "I sit down.", "id": "c14", "status": "done"}`
── beat 17
→ `{"id": "c15", "domain": "dog", "verb": "LOCAL_ACTION", "args": {"action": "TURN", "direction": "LEFT", "angle_deg": 90}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 16}, "text": "Accepted.", "id": "c15", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 34, "beat": 17, "event": "turn_must_stand[S=left,P=sitting]"}, "text": "I can't turn: I have to stand up first.", "id": "c15", "status": "refused", "reason": "I have to stand up first", "alternatives": ["stand"], "blocker": null}`
── beat 18
── beat 19
    ← `{"domain": "dog", "kind": "quiescence", "ref": {"seq": 35, "beat": 18}, "text": "the day is over", "goal_pending": false}`
── the host's episode is over (beat 18); the REPL API after the day
→ `{"id": "t1", "domain": "dog", "tool": "why", "args": {"atom": "done(go)"}}`
    ← `{"domain": "dog", "kind": "read", "ref": {"seq": 35, "beat": 18}, "tool": "why", "output": "why done(go) (last produced by event 12) \u2014 not present now: 13 of 36 occurrences \u2026"}`
→ `{"id": "t2", "domain": "dog", "tool": "why", "args": {"atom": "refused(find)"}}`
    ← `{"domain": "dog", "kind": "read", "ref": {"seq": 35, "beat": 18}, "tool": "why", "output": "why refused(find) (last produced by event 30) \u2014 not present now: 31 of 36 occurrences \u2026"}`
→ `{"id": "t3", "domain": "dog", "tool": "state", "args": {}}`
    ← `{"domain": "dog", "kind": "read", "ref": {"seq": 35, "beat": 18}, "tool": "state", "output": "stage (done) \u2026"}`
