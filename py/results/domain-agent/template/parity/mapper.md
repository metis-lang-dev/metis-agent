# the dog's mapper on the 54 corpus rows (no model)

The Go pass-1 JSON of each row (fsttm-web test/fixtures/harness-e2b.jsonl) as an intent: verb = its `intent`, args = the rest. **54/54 mapped to a command atom**, 0 the mapper's error.

| # | utterance | src | verb | args | command atom | dropped |
|---|---|---|---|---|---|---|
| 1 | stand up | prompt | LOCAL_ACTION | `{"action":"STAND_UP"}` | `cmd_stand` |  |
| 2 | sit | prompt | LOCAL_ACTION | `{"action":"SIT_DOWN"}` | `cmd_sit` |  |
| 3 | lie down | prompt | LOCAL_ACTION | `{"action":"LIE_DOWN"}` | `cmd_lie` |  |
| 4 | lay down | prompt | LOCAL_ACTION | `{"action":"LIE_DOWN"}` | `cmd_lie` |  |
| 5 | turn left ninety degrees | prompt | LOCAL_ACTION | `{"action":"TURN","direction":"LEFT","angle_deg":90}` | `cmd_turn(left)` | angle_deg=90 |
| 6 | turn around | prompt | LOCAL_ACTION | `{"action":"TURN","direction":"AROUND"}` | `cmd_turn(around)` |  |
| 7 | take a step forward | prompt | LOCAL_ACTION | `{"action":"MOVE","direction":"FORWARD"}` | `cmd_move(forward)` |  |
| 8 | go one meter forward | prompt | LOCAL_ACTION | `{"action":"MOVE","direction":"FORWARD","distance_m":1}` | `cmd_move(forward)` | distance_m=1 |
| 9 | back up half a meter | prompt | LOCAL_ACTION | `{"action":"MOVE","direction":"BACKWARD","distance_m":0.5}` | `cmd_move(backward)` | distance_m=0.5 |
| 10 | take a step back | prompt | LOCAL_ACTION | `{"action":"MOVE","direction":"BACKWARD"}` | `cmd_move(backward)` |  |
| 11 | give me your paw | prompt | LOCAL_ACTION | `{"action":"SHAKE"}` | `cmd_paw` |  |
| 12 | wave at me | prompt | LOCAL_ACTION | `{"action":"SHAKE"}` | `cmd_paw` |  |
| 13 | where is the chair | prompt | QUERY | `{"target":{"type":"OBJECT","description":"chair"}}` | `cmd_query(chair)` |  |
| 14 | what is next to the table | prompt | QUERY | `{"target":{"type":"OBJECT","description":"table"},"constraints":[{"relation":"NEXT_TO","reference":{"type":"OBJECT","description":"table"}}]}` | `cmd_query(table)` | constraints=[{"relation":"NEXT_TO","reference":{"type":"OBJECT","description":"table"}}] |
| 15 | go to the door | prompt | NAVIGATE | `{"goal":{"type":"OBJECT","description":"door"}}` | `cmd_go(door)` |  |
| 16 | go to the chair next to the window | prompt | NAVIGATE | `{"goal":{"type":"OBJECT","description":"chair"},"constraints":[{"relation":"NEXT_TO","reference":{"type":"OBJECT","description":"window"}}]}` | `cmd_go(window_chair)` |  |
| 17 | go to the kitchen | prompt | NAVIGATE | `{"goal":{"type":"REGION","description":"kitchen"}}` | `cmd_go(kitchen)` |  |
| 18 | go to tag five | prompt | NAVIGATE | `{"goal":{"type":"TAG","description":"tag"},"tag_id":5}` | `cmd_go(tag5)` |  |
| 19 | walk to april tag three | prompt | NAVIGATE | `{"goal":{"type":"TAG","description":"april tag"},"tag_id":3}` | `cmd_go(tag3)` |  |
| 20 | go to the tag | prompt | NAVIGATE | `{"goal":{"type":"TAG","description":"tag"}}` | `cmd_go(tag_near)` |  |
| 21 | find a fire extinguisher | prompt | FIND | `{"target":{"type":"OBJECT","description":"fire extinguisher"}}` | `cmd_find(extinguisher)` |  |
| 22 | go find the red chair near the window | prompt | FIND | `{"target":{"type":"OBJECT","description":"chair","attributes":{"color":"red"}},"constraints":[{"relation":"NEAR","reference":{"type":"OBJECT","description":"window"}}]}` | `cmd_find(chair)` | constraints=[{"relation":"NEAR","reference":{"type":"OBJECT","description":"window"}}], target.attributes={"color":"red"} |
| 23 | follow me | prompt | FOLLOW | `{"target":{"type":"PERSON","description":"the person"}}` | `cmd_follow` | target={"type":"PERSON","description":"the person"} |
| 24 | stop | prompt | STOP | `{}` | `cmd_stop` |  |
| 25 | never mind | prompt | CANCEL | `{}` | `cmd_cancel` |  |
| 26 | what time is it | prompt | TIME | `{}` | `cmd_time` |  |
| 27 | good boy | prompt | CHITCHAT | `{}` | `cmd_chat` |  |
| 28 | order me a pizza | prompt | UNKNOWN | `{}` | `cmd_unknown` |  |
| 29 | do a front flip | prompt | UNKNOWN | `{}` | `cmd_unknown` |  |
| 30 | do a backflip | prompt | UNKNOWN | `{}` | `cmd_unknown` |  |
| 31 | roll over | prompt | UNKNOWN | `{}` | `cmd_unknown` |  |
| 32 | dance | prompt | UNKNOWN | `{}` | `cmd_unknown` |  |
| 33 | jump | prompt | UNKNOWN | `{}` | `cmd_unknown` |  |
| 34 | pounce | prompt | UNKNOWN | `{}` | `cmd_unknown` |  |
| 35 | could you sit down please | heldout | LOCAL_ACTION | `{"action":"SIT_DOWN"}` | `cmd_sit` |  |
| 36 | get up | heldout | LOCAL_ACTION | `{"action":"STAND_UP"}` | `cmd_stand` |  |
| 37 | turn right | heldout | LOCAL_ACTION | `{"action":"TURN","direction":"RIGHT"}` | `cmd_turn(right)` |  |
| 38 | move forward two meters | heldout | LOCAL_ACTION | `{"action":"MOVE","direction":"FORWARD","distance_m":2}` | `cmd_move(forward)` | distance_m=2 |
| 39 | shake hands | heldout | LOCAL_ACTION | `{"action":"SHAKE"}` | `cmd_paw` |  |
| 40 | is there a chair around here | heldout | QUERY | `{"target":{"type":"OBJECT","description":"chair"}}` | `cmd_query(chair)` |  |
| 41 | what do you see near the window | heldout | QUERY | `{"target":{"type":"OBJECT","description":"thing"}}` | `cmd_look(elsewhere)` |  |
| 42 | head over to the kitchen | heldout | NAVIGATE | `{"goal":{"type":"REGION","description":"kitchen"}}` | `cmd_go(kitchen)` |  |
| 43 | go to april tag seven | heldout | NAVIGATE | `{"goal":{"type":"TAG","description":"april tag"},"tag_id":7}` | `cmd_go(elsewhere)` | tag_id=7 |
| 44 | walk to the sofa | heldout | NAVIGATE | `{"goal":{"type":"OBJECT","description":"sofa"}}` | `cmd_go(sofa)` |  |
| 45 | look for my keys | heldout | FIND | `{"target":{"type":"OBJECT","description":"keys"}}` | `cmd_find(keys)` |  |
| 46 | search for a blue bottle | heldout | FIND | `{"target":{"type":"OBJECT","description":"bottle","attributes":{"color":"blue"}}}` | `cmd_find(bottle)` | target.attributes={"color":"blue"} |
| 47 | come with me | heldout | FOLLOW | `{"target":{"type":"PERSON","description":"the person"}}` | `cmd_follow` | target={"type":"PERSON","description":"the person"} |
| 48 | halt | heldout-in-prompt | STOP | `{}` | `cmd_stop` |  |
| 49 | forget it | heldout-in-prompt | CANCEL | `{}` | `cmd_cancel` |  |
| 50 | what's the date today | heldout-in-prompt | DATE | `{}` | `cmd_date` |  |
| 51 | hi there buddy | heldout | CHITCHAT | `{}` | `cmd_chat` |  |
| 52 | make me a coffee | heldout | UNKNOWN | `{}` | `cmd_unknown` |  |
| 53 | do a somersault | heldout | UNKNOWN | `{}` | `cmd_unknown` |  |
| 54 | play dead | heldout | UNKNOWN | `{}` | `cmd_unknown` |  |
