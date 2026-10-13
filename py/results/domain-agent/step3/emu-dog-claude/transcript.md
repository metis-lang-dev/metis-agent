# orchestrator emulator — dog_host.py's SCRIPT as intents: the person's command of each beat, typed as the executive types it (go/grammar/dog.gbnf); the world's changes and answers stay the host's. After the day, the REPL API reads of dog-transcript.md.

── ready; beat 1 (intents sent now are asserted at its tick)
→ `{"id": "c1", "domain": "dog", "verb": "LOCAL_ACTION", "args": {"action": "SIT_DOWN"}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 0}, "text": "Accepted.", "id": "c1", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 2, "beat": 1, "event": "sit[P=standing]"}, "text": "The dog sat down.", "id": "c1", "status": "done"}`
── beat 2
→ `{"id": "c2", "domain": "dog", "verb": "NAVIGATE", "args": {"goal": {"type": "REGION", "description": "kitchen"}}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 1}, "text": "Accepted.", "id": "c2", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 4, "beat": 2, "event": "go_closed[P=kitchen]"}, "text": "The dog could not go to the kitchen.", "id": "c2", "status": "refused", "reason": "the way to it is closed", "alternatives": ["door", "tag3"]}`
── beat 3
→ `{"id": "o1", "domain": "dog", "tool": "consult", "args": {"question": "Why the way to it is closed?", "max_calls": 6}}`
    ← `{"domain": "dog", "kind": "consult", "ref": {"seq": 4, "beat": 2}, "text": "The go command for the kitchen (event 4) was refused by go_closed[P=kitchen] because the kitchen is mapped but closed(kitchen) holds, and it has held since the base state. Nothing in the run closed it. To set off, the world needed open(kitchen), and the dog would also have to be standing first since it is sitting.", "id": "o1", "answer_kind": "missing", "resource": {"atom": "open(kitchen)", "words": "an open way to the kitchen"}, "calls": 2}`
→ `{"id": "c3", "domain": "dog", "verb": "LOCAL_ACTION", "args": {"action": "STAND_UP"}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 2}, "text": "Accepted.", "id": "c3", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 6, "beat": 3, "event": "stand[P=sitting]"}, "text": "The dog stood up.", "id": "c3", "status": "done"}`
── beat 4
→ `{"id": "c4", "domain": "dog", "verb": "NAVIGATE", "args": {"goal": {"type": "TAG", "description": "april tag"}, "tag_id": 3}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 3}, "text": "Accepted.", "id": "c4", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 8, "beat": 4, "event": "go[P=tag3]"}, "text": "The dog is going to the place you named.", "id": "c4", "status": "progress"}`
── beat 5
→ `{"id": "c5", "domain": "dog", "verb": "FIND", "args": {"target": {"type": "OBJECT", "description": "keys"}}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 4}, "text": "Accepted.", "id": "c5", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 10, "beat": 5, "event": "find_busy[T=keys,P=tag3]"}, "text": "The dog refused to search for the keys because it was already busy.", "id": "c5", "status": "refused", "reason": "I am already busy with a task", "alternatives": []}`
── beat 6
→ `{"id": "o2", "domain": "dog", "tool": "consult", "args": {"question": "Why I am already busy with a task?", "max_calls": 6}}`
    ← `{"domain": "dog", "kind": "consult", "ref": {"seq": 10, "beat": 5}, "text": "The search for the keys was refused by find_busy[T=keys,P=tag3] (event 10) because the dog was still on its way to tag3. Event 8, go[P=tag3], used up idle and left moving(tag3), so the find move had no idle to start from. The dog has to arrive, be stopped or be cancelled before it can search.", "id": "o2", "answer_kind": "missing", "resource": {"atom": "idle", "words": "being free of any other task (idle)"}, "calls": 1}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 12, "beat": 6, "event": "arrive[P=tag3,Q=door]"}, "text": "The dog has arrived at tag3.", "id": "c4", "status": "done"}`
── beat 7
→ `{"id": "c6", "domain": "dog", "verb": "LOCAL_ACTION", "args": {"action": "MOVE", "direction": "BACKWARD"}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 6}, "text": "Accepted.", "id": "c6", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 14, "beat": 7, "event": "move_wall[W=backward]"}, "text": "The dog cannot move that way because there is a wall right there.", "id": "c6", "status": "refused", "reason": "there is a wall right there", "alternatives": []}`
── beat 8
→ `{"id": "o3", "domain": "dog", "tool": "consult", "args": {"question": "Why there is a wall right there?", "max_calls": 6}}`
    ← `{"domain": "dog", "kind": "consult", "ref": {"seq": 14, "beat": 7}, "text": "No move put the wall there: wall(backward) has been in the world since the base state (#init), and none of the 14 events produced it. When you asked the dog to move backward, it needed a clear way that direction. The world only has clear(forward), so with wall(backward) present it refused at event 14, move_wall[W=backward].", "id": "o3", "answer_kind": "missing", "resource": {"atom": "clear(backward)", "words": "a clear way backward"}, "calls": 1}`
→ `{"id": "c7", "domain": "dog", "verb": "FIND", "args": {"target": {"type": "OBJECT", "description": "fire extinguisher"}}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 7}, "text": "Accepted.", "id": "c7", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 16, "beat": 8, "event": "find[T=extinguisher]"}, "text": "The dog started looking for the extinguisher.", "id": "c7", "status": "progress"}`
── beat 9
→ `{"id": "c8", "domain": "dog", "verb": "QUERY", "args": {"target": {"type": "OBJECT", "description": "chair"}}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 8}, "text": "Accepted.", "id": "c8", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 18, "beat": 9, "event": "query_seen[T=chair,P=kitchen]"}, "text": "The dog told you where it saw the chair in the kitchen.", "id": "c8", "status": "done"}`
── beat 10
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 20, "beat": 10, "event": "find_locked[T=extinguisher]"}, "text": "The dog could not find the locked item.", "id": "c7", "status": "refused", "reason": "it is locked away", "alternatives": []}`
── beat 11
→ `{"id": "o4", "domain": "dog", "tool": "consult", "args": {"question": "Why it is locked away?", "max_calls": 6}}`
    ← `{"domain": "dog", "kind": "consult", "ref": {"seq": 20, "beat": 10}, "text": "The model has no reason for the extinguisher being locked away: locked_away(extinguisher) was added from outside at step 19 ('do +locked_away(extinguisher)'), and no move in the dog's script produces it. The model only shows what happened next: at event 20, find_locked[T=extinguisher] ended the search that find[T=extinguisher] had started at event 16, and the dog said 'I can't reach it: it is locked away.'", "id": "o4", "answer_kind": "unexplained", "calls": 1}`
→ `{"id": "c9", "domain": "dog", "verb": "FOLLOW", "args": {"target": {"type": "PERSON", "description": "the person"}}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 10}, "text": "Accepted.", "id": "c9", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 22, "beat": 11, "event": "follow"}, "text": "The dog is following you.", "id": "c9", "status": "progress"}`
── beat 12
→ `{"id": "c10", "domain": "dog", "verb": "STOP", "args": {}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 11}, "text": "Accepted.", "id": "c10", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 24, "beat": 12, "event": "stop_follow"}, "text": "The dog stopped following you.", "id": "c9", "status": "cancelled", "reason": "I stop following you"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 24, "beat": 12, "event": "stop_follow"}, "text": "The dog stopped following you.", "id": "c10", "status": "done"}`
── beat 13
→ `{"id": "c11", "domain": "dog", "verb": "UNKNOWN", "args": {}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 12}, "text": "Accepted.", "id": "c11", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 26, "beat": 13, "event": "unknown"}, "text": "The dog refused to do something unknown.", "id": "c11", "status": "refused", "reason": "it is not something I can or may do", "alternatives": []}`
── beat 14
→ `{"id": "o5", "domain": "dog", "tool": "consult", "args": {"question": "Why it is not something I can or may do?", "max_calls": 6}}`
    ← `{"domain": "dog", "kind": "consult", "ref": {"seq": 26, "beat": 13}, "text": "Event 26 was the unknown move, which fired on the command alone. The command came in as cmd unknown, and this move requires nothing else from the world. It is the fixed reply for any request outside the dog's repertoire: no state was lacking, so no missing resource explains it.", "id": "o5", "answer_kind": "unexplained", "calls": 3}`
→ `{"id": "c12", "domain": "dog", "verb": "LOCAL_ACTION", "args": {"action": "SHAKE"}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 13}, "text": "Accepted.", "id": "c12", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 28, "beat": 14, "event": "paw"}, "text": "The dog gave you its paw.", "id": "c12", "status": "done"}`
── beat 15
→ `{"id": "c13", "domain": "dog", "verb": "FIND", "args": {"target": {"type": "OBJECT", "description": "bottle", "attributes": {"color": "blue"}}}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 14}, "text": "Accepted.", "id": "c13", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 30, "beat": 15, "event": "find_low[T=bottle]"}, "text": "The dog could not search because the battery was low.", "id": "c13", "status": "refused", "reason": "the battery is too low to search", "alternatives": []}`
── beat 16
→ `{"id": "o6", "domain": "dog", "tool": "consult", "args": {"question": "Why the battery is too low to search?", "max_calls": 6}}`
    ← `{"domain": "dog", "kind": "consult", "ref": {"seq": 30, "beat": 15}, "text": "The search command was refused at event 30 (find_low[T=bottle]). The find move needs battery_ok, and the world has battery_low but no battery_ok. battery_low was last produced by event 29, which comes after the earlier find at event 16 that ran while battery_ok still held.", "id": "o6", "answer_kind": "missing", "resource": {"atom": "battery_ok", "words": "enough battery charge to search"}, "calls": 5}`
→ `{"id": "c14", "domain": "dog", "verb": "LOCAL_ACTION", "args": {"action": "SIT_DOWN"}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 15}, "text": "Accepted.", "id": "c14", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 32, "beat": 16, "event": "sit[P=standing]"}, "text": "The dog sat down.", "id": "c14", "status": "done"}`
── beat 17
→ `{"id": "c15", "domain": "dog", "verb": "LOCAL_ACTION", "args": {"action": "TURN", "direction": "LEFT", "angle_deg": 90}}`
    ← `{"domain": "dog", "kind": "result", "ref": {"beat": 16}, "text": "Accepted.", "id": "c15", "status": "accepted"}`
    ← `{"domain": "dog", "kind": "result", "ref": {"seq": 34, "beat": 17, "event": "turn_must_stand[S=left,P=sitting]"}, "text": "The dog cannot turn because it must stand up first.", "id": "c15", "status": "refused", "reason": "I have to stand up first", "alternatives": []}`
── beat 18
→ `{"id": "o7", "domain": "dog", "tool": "consult", "args": {"question": "Why I have to stand up first?", "max_calls": 6}}`
    ← `{"domain": "dog", "kind": "consult", "ref": {"seq": 34, "beat": 17}, "text": "Turning needs me to be standing, but I am sitting: event 32 left me in posture(sitting), so the turn came out as turn_must_stand at event 34. The way to my left is free (free(left)), so standing is the only thing missing. Also note that my battery is now low (battery_low), so a stand command would currently be refused as stand_low.", "id": "o7", "answer_kind": "missing", "resource": {"atom": "posture(standing)", "words": "being up on my feet"}, "calls": 4}`
── beat 19
    ← `{"domain": "dog", "kind": "quiescence", "ref": {"seq": 35, "beat": 18}, "text": "The dog is still.", "goal_pending": false}`
── the host's episode is over (beat 18); the REPL API after the day
→ `{"id": "t1", "domain": "dog", "tool": "why", "args": {"atom": "done(go)"}}`
    ← `{"domain": "dog", "kind": "read", "ref": {"seq": 35, "beat": 18}, "tool": "why", "output": "why done(go) (last produced by event 12) \u2014 not present now: 13 of 36 occurrences \u2026"}`
→ `{"id": "t2", "domain": "dog", "tool": "why", "args": {"atom": "refused(find)"}}`
    ← `{"domain": "dog", "kind": "read", "ref": {"seq": 35, "beat": 18}, "tool": "why", "output": "why refused(find) (last produced by event 30) \u2014 not present now: 31 of 36 occurrences \u2026"}`
→ `{"id": "t3", "domain": "dog", "tool": "state", "args": {}}`
    ← `{"domain": "dog", "kind": "read", "ref": {"seq": 35, "beat": 18}, "tool": "state", "output": "stage (done) \u2026"}`
