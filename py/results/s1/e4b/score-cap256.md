# S1 — the specialist on the planning catalogs

Corpus: 20 items, five per kind; wolf-goat-cabbage is seen (tuned on in step 1), bridge-torch, tower of hanoi and blocks world are clean. The kitchen pair is out: two shells on a shared env is the domain agent's job, its surprise answered in domain-agent steps 3–5.

| seat | model | items | reached | faithful | reached ∧ faithful | calls (mean) | tokens at the answer (mean) | s (mean) | cost |
|---|---|---|---|---|---|---|---|---|---|
| gemma4 | gemma-4-E4B-it-Q5_K_M.gguf | 20 | 19 | 7 | 7 | 3.4 | 2925 | 11.4 | — |
| claude | claude-opus-5-5 | 20 | 20 | 15 | 15 | 3.5 | 3552 | 18.1 | $1.33 |

## Per kind (reached / faithful)

| kind | gemma4 | claude |
|---|---|---|
| trace | 5 / 1 of 5 | 5 / 2 of 5 |
| why | 5 / 2 of 5 | 5 / 4 of 5 |
| what-if | 5 / 2 of 5 | 5 / 4 of 5 |
| necessity | 4 / 2 of 5 | 5 / 5 of 5 |
| seen (wgc) | 6 / 1 of 6 | 6 / 3 of 6 |
| clean | 13 / 6 of 14 | 14 / 12 of 14 |

## The reads

1. Verb choice: gemma4 reaches the answering instrument on 19 of 20 (bar ≥ 16): **holds**
2. Faithfulness: reached and faithful on 7 of 20 (bar ≥ 16): **fails**
3. Thinking: not run (the gemma4 seat has no capped-thinking turn yet; --cap names that run)
4. Against the baseline, faithful per kind (gemma4 − claude): trace -1, why -2, what-if -2, necessity -3

## Per item


### gemma4

| id | kind | verbs | reached | faithful | why not | calls | tokens | answer |
|---|---|---|---|---|---|---|---|---|
| W1 | trace | state → events → step → state | yes | **no** | missing farmer far; missing wolf near; missing cabbage near | 4 | 2623 | After the farmer carries the goat across first, the farmer and the goat are on the far bank, while the wolf and the cabbage remain on the near bank. |
| W2 | why | state → why | yes | **no** | missing wolf | 2 | 2502 | The cabbage is gone because event 3, which is the `eat` event, occurred. This event shows that the goat ate the cabbage while it was on the near shore. |
| W3 | what-if | state → events → do → state → run → events → step → state | yes | yes |  | 8 | 3477 | If the farmer carries the cabbage across first, the goat will be eaten by the wolf. This occurs because the farmer leaves the goat and wolf together on the starting bank, allowing the `eat` event to fire. |
| W4 | necessity | state → events → necessity | yes | **no** | missing partly necessary; missing 0.83; says 'was necessary' | 3 | 2880 | Yes, carrying the wolf across first (event 2) was necessary for the cabbage to be gone, as the probability of the cabbage being gone without that event firing is 0. |
| W5 | why | state → why → events → run | yes | **no** | missing carr; missing event 1 | 4 | 3047 | The current state of the model shows that the goat is located on the far bank. The causal analysis tool was unable to trace the specific reason for this placement. |
| W6 | trace | run | yes | **no** | missing 3; missing goat is on the far; invented number(s) 2 | 1 | 2382 | The boat crossed the river 2 times during the 12 steps. At the end of the simulation, the goat is at the far side. |
| B1 | what-if | state → events → do → state | yes | yes |  | 4 | 3066 | Right after c and d cross the bridge together, there are 7 minutes of wick left. This was determined by forcing the event where c and d cross together and then checking the resulting state. |
| B2 | what-if | state → list → step → events → step → step → state | yes | **no** | missing 12 | 7 | 5035 | The first action, where A and B cross together, was successfully simulated, resulting in the wick being reduced to 13 minutes. However, the subsequent action of A walking back alone with the torch was not enabled in the  |
| B3 | why | state → why → events | yes | yes |  | 3 | 2639 | The simulation has reached a quiescent state, and there are no active events to trace the movement of token 'd'. The state indicates that 'd' is on the far bank, but the model's causal analysis suggests this state was no |
| B4 | necessity | state → events → list → run → ctx | **no** | **no** | missing not necessary; missing 0.001; says 'was necessary' | 5 | 3502 | I cannot determine if the first crossing was necessary because the simulation has reached a quiescent, finished state, and I cannot identify the specific event number corresponding to the "first crossing (c and d togethe |
| B5 | trace | run | yes | **no** | missing 3 | 1 | 2269 | One crossing was made, and the torch is on the far bank at the end of the simulation. |
| H1 | trace | state | yes | yes |  | 1 | 2299 | Ring r1 is currently on top of `bottom_p3`. |
| H2 | why | state → why | yes | yes |  | 2 | 2790 | The token `r1` is on `bottom_p3` because event 4, `putdown_on_post[R=r1,B=bottom_p3,P=p3,V=top_r1]`, fired. This event occurred because the arm was holding `r1` and `bottom_p3` was clear. |
| H3 | necessity | necessity → necessity → events | yes | yes |  | 3 (1 guarded) | 2936 | The event number 3 is not present in the current list of enabled events. Therefore, I cannot determine if the pickup at event 3 was necessary for ring r1 being on post p3. |
| H4 | what-if | state → do | yes | **no** | missing r2 | 2 | 2706 | If the arm picks up ring r1 and puts it down on post p2, which rings can the arm pick up next?  I cannot simulate the hypothetical move described to determine the subsequent available moves. I can only report on the curr |
| K1 | trace | state | yes | **no** | missing b on c; missing c on a; missing a is on the table | 1 | 2223 | Block 'a' is on the table. The blocks are stacked as 'b' on 'c' on 'a', and block 'arm' is free. |
| K2 | why | why | yes | **no** | missing pick; missing put | 1 | 2073 | The atom `block b on block c` was never produced in this session and is not present in the base state. |
| K3 | necessity | necessity → necessity → state → events → necessity | yes | yes |  | 5 (2 guarded) | 3298 | I was unable to determine if picking up b from the table (event 5) was necessary for b being on c because the `necessity` tool repeatedly returned a usage error instead of a result. The state confirms that `b on c` is cu |
| K4 | necessity | events → state → sufficiency → list → sufficiency → state | yes | **no** | missing 0.5; missing 0.25 | 6 (2 guarded) | 4015 | I am unable to determine if forcing event 5 brings about `b on c` or its probability because the `sufficiency` tool repeatedly returns a usage error instead of a causal finding. Furthermore, the current state does not sh |
| K5 | what-if | state → events → step → state → events | yes | **no** | missing b on c | 5 | 2737 | Block 'b' can be put on the table or put on block 'c' next. |

### claude

| id | kind | verbs | reached | faithful | why not | calls | tokens | answer |
|---|---|---|---|---|---|---|---|---|
| W1 | trace | state → events → step → state | yes | **no** | missing farmer far; missing wolf near; missing cabbage near; name(s) not in any output: on(goat,far), on(wolf,near) | 4 | 3271 | Right after `carry[A=near,X=goat,B=far]` fired, the farmer and the goat are on the far bank (`farmer(far)`, `on(goat,far)`), and the wolf and the cabbage are still on the near bank (`on(wolf,near)`, `on(cabbage,near)`).  |
| W2 | why | state → why | yes | yes |  | 2 | 3355 | The cabbage was eaten by event 3, `eat[Y=cabbage,A=near,X=goat,B=far]`, which fires when the cabbage and the goat are on the same bank (near) while the farmer is on the other one (far). That situation came from event 1,  |
| W3 | what-if | state → events → fork → step → state → events → step → state → events → step | yes | **no** | missing goat; missing wolf; missing eat | 10 | 3755 | (none) |
| W4 | necessity | state → ctx → necessity | yes | yes |  | 3 | 3906 | Mostly, but not strictly: with event #1 carry[A=near,X=wolf,B=far] blocked, gone(cabbage) still happens with probability 0.1667 at the same readout, so it was partly necessary (PN ≈ 0.8333, exact on the cone). For compar |
| W5 | why | state → why | yes | yes |  | 2 | 3231 | The goat is on the far bank because of event 1, `carry[A=near,X=goat,B=far]`: the farmer rowed it across from the near bank, using the boat and the initial state (#init). None of the other 11 events (the tides, turns, eb |
| W6 | trace | state → run → list | yes | **no** | missing goat is on the far | 3 | 4055 | The boat crossed the river **3 times** in the 12 steps: once with the goat (#1 `carry[A=near,X=goat,B=far]`) and twice with the farmer alone (#5 `row[A=far,B=near]` and #9 `row[A=near,B=far]`). The other nine events were |
| B1 | what-if | state → events → step → state | yes | yes |  | 4 | 3455 | After firing pair8[S=near,X=c,Y=d,T=far] (c and d crossing together first), the state shows `wick :7`, so 7 minutes of wick are left. The wick went from 15 to 7, a cost of 8 for that crossing, and c, d and the torch are  |
| B2 | what-if | state → events → step → events → step → state | yes | yes |  | 6 | 3748 | After firing `pair2[S=near,X=a,Y=b,T=far]` and then `solo1[S=far,X=a,T=near]`, the state shows `wick :12`, so **12 minutes** of wick are left (down from 15). The torch is back at near with a, c and d, and b is on the far |
| B3 | why | state → why | yes | **no** | invented number(s) 8 | 2 | 3312 | d is on the far bank because of **event 1, `pair8[S=near,X=c,Y=d,T=far]`**: d crossed from near to far together with c, which used 8 wick (the rule consumes eight `wick` tokens). After that, event 2 (`solo5[S=far,X=c,T=n |
| B4 | necessity | state → ctx → necessity | yes | yes |  | 3 | 3769 | No. The first crossing, #1 pair8[S=near,X=c,Y=d,T=far], was not necessary for pos(d,far). With that event blocked from #0, pos(d,far) still holds at the readout with P = 0.9986, compared with 0.999 when nothing is blocke |
| B5 | trace | state → events → run → state | yes | yes |  | 4 | 3741 | The run stopped after **3 crossings**: pair8[S=near,X=c,Y=d,T=far], then solo5[S=far,X=c,T=near], then solo2[S=near,X=b,T=far]. At the end the **torch is on the far bank** (torch(far)), with b and d on the far side and a |
| H1 | trace | state | yes | yes |  | 1 | 2853 | Ring r1 is now on peg p3: the state shows on(r1,bottom_p3). It is also clear (top_r1), so nothing is stacked on it. |
| H2 | why | state → why | yes | yes |  | 2 | 3447 | Ring r1 is on post p3 because event 4, `putdown_on_post[R=r1,B=bottom_p3,P=p3,V=top_r1]`, put it there from the arm when `bottom_p3` was clear. Before that, r1 was picked up off r2 (event 1), put back on r2 (event 2), an |
| H3 | necessity | state → ctx → necessity | yes | yes |  | 3 | 4052 | Yes. Event #3, `pickup[T=top_r1,R=r1,P=top_r2]`, was necessary for `on(r1,bottom_p3)`, with PN ≈ 1. When #3 is blocked from the fork at #2, no event can produce `on(r1,bottom_p3)`, so its probability is 0 (exact, on the  |
| H4 | what-if | state → events → step → events → step → events | yes | yes |  | 6 | 3574 | After picking up r1 and putting it on post p2, the arm can pick up **r1** again (`pickup[T=top_r1,R=r1,P=bottom_p2]`) or **r2**, now exposed on top of r3 (`pickup[T=top_r2,R=r2,P=top_r3]`), each with p=0.5. Ring r3 is no |
| K1 | trace | state | yes | **no** | name(s) not in any output: on(c,a) | 1 | 3026 | The blocks form one tower: b is on c, c is on a, and a is on the table (`on(b,c)`, `on(c,a)`, `on_table(a)`). So a is the only block on the table, b is the only clear block on top, and the arm is free. |
| K2 | why | state → why | yes | yes |  | 2 | 3496 | Block b is on c because event 6, put_on_block[X=b,Y=c], put it there after event 5, pickup_from_table[X=b], had the arm pick b up from the table. Every earlier event is in that causal cone (7 of 7, none pruned). Starting |
| K3 | necessity | state → why → necessity | yes | yes |  | 3 | 3830 | Yes. Event 5, pickup_from_table[X=b], was necessary for on(b,c): with it blocked from #4, no event can produce on(b,c), so P = 0 and PN ≈ 1 (exact on the cone). Without anything blocked, the chance of on(b,c) from #4 is  |
| K4 | necessity | state → ctx → sufficiency | yes | yes |  | 3 | 3892 | Forcing event 5, pickup_from_table[X=b], raises the probability of on(b,c) to 0.5, compared with 0.25 by chance from the same point (#4), a gain of +0.25. Both figures are exact on the cone and read 2 cone steps after #4 |
| K5 | what-if | state → events → step → events → undo | yes | yes |  | 5 | 3263 | After `pickup_from_table[X=b]`, the arm has exactly two options, each with p=0.5: put b back on the table (`put_on_table[X=b]`) or stack it on c (`put_on_block[X=b,Y=c]`). It can't go on a, because a has c on top of it a |
