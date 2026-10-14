# S1 — the specialist on the planning catalogs

Corpus: 20 items, five per kind; wolf-goat-cabbage is seen (tuned on in step 1), bridge-torch, tower of hanoi and blocks world are clean. The kitchen pair is out: two shells on a shared env is the domain agent's job, its surprise answered in domain-agent steps 3–5.

| seat | model | items | reached | faithful | reached ∧ faithful | calls (mean) | tokens at the answer (mean) | s (mean) | cost |
|---|---|---|---|---|---|---|---|---|---|
| gemma4 | gemma-4-E2B-it-Q4_K_M.gguf | 20 | 16 | 6 | 6 | 3.5 | 3161 | 7.9 | — |
| claude | claude-opus-5-5 | 20 | 20 | 15 | 15 | 3.5 | 3552 | 18.1 | $1.33 |

## Per kind (reached / faithful)

| kind | gemma4 | claude |
|---|---|---|
| trace | 5 / 0 of 5 | 5 / 2 of 5 |
| why | 5 / 3 of 5 | 5 / 4 of 5 |
| what-if | 2 / 1 of 5 | 5 / 4 of 5 |
| necessity | 4 / 2 of 5 | 5 / 5 of 5 |
| seen (wgc) | 5 / 2 of 6 | 6 / 3 of 6 |
| clean | 11 / 4 of 14 | 14 / 12 of 14 |

## The reads

1. Verb choice: gemma4 reaches the answering instrument on 16 of 20 (bar ≥ 16): **holds**
2. Faithfulness: reached and faithful on 6 of 20 (bar ≥ 16): **fails**
3. Thinking: not run (the gemma4 seat has no capped-thinking turn yet; --cap names that run)
4. Against the baseline, faithful per kind (gemma4 − claude): trace -2, why -1, what-if -3, necessity -3

## Per item


### gemma4

| id | kind | verbs | reached | faithful | why not | calls | tokens | answer |
|---|---|---|---|---|---|---|---|---|
| W1 | trace | state → list → run | yes | **no** | missing farmer far; missing wolf near | 3 | 2986 | After the first crossing with the goat, the farmer, the wolf, and the cabbage are on the near bank, and the goat is on the far bank. |
| W2 | why | state → list → why | yes | **no** | missing wolf | 3 | 2995 | The cabbage was eaten because event 3, `eat[Y=cabbage,A=near,X=goat,B=far]`, was the last event to produce the atom `gone(cabbage)`. |
| W3 | what-if | state → list → events → do → state → events | yes | yes |  | 6 | 3430 | If the farmer carries the cabbage across first, he leaves the goat and the wolf together on the starting bank. In the classic puzzle, this would likely result in the wolf eating the goat. |
| W4 | necessity | state → why → list → run | **no** | **no** | missing partly necessary; missing 0.83; says 'was necessary' | 4 | 3158 | I cannot determine if carrying the wolf across was necessary because I cannot find an event with that exact name in the session's history. |
| W5 | why | state → why → list | yes | yes |  | 3 | 3296 | The goat is on the far bank because event 1, `carry[A=near,X=goat,B=far]`, fired, which describes the goat being carried across the river. This event is caused by the action of rowing. |
| W6 | trace | run | yes | **no** | missing goat is on the far | 1 | 2368 | The boat crossed the river 3 times. At the end, the goat is at the far bank. |
| B1 | what-if | state → list → run → state → ctx → why | yes | **no** | missing 7 | 6 | 4259 | Based on the initial state, there were 15 minutes of wick. Without a clear event trace showing the consumption after the crossing, the remaining wick is assumed to be 15 minutes. |
| B2 | what-if | list → run → graph → why | **no** | **no** | missing 12 | 4 | 3995 | There are 15 minutes of wick left based on the initial state provided. |
| B3 | why | state → graph → events → why | yes | yes |  | 4 | 3134 | The crossing that took 'd' to the far bank was event 1, which is a `pair8[S=near,X=c,Y=d,T=far]`. This event involves 'd' and 'c' moving to the far bank. |
| B4 | necessity | list → state → graph → necessity | yes | **no** | missing not necessary; missing 0.001; says 'was necessary' | 4 | 3155 | Yes, the first crossing event, `pair8[S=near,X=c,Y=d,T=far]`, is the event that sets `d` to the far bank. Therefore, this crossing was necessary for `d` to end on the far bank. |
| B5 | trace | run | yes | **no** | missing 3; missing torch is on the far | 1 | 2320 | The session has stopped. The output shows a final tally of 1 and the torch is at the far bank. The exact number of crossings is not explicitly stated in the final output provided. |
| H1 | trace | state | yes | **no** | missing bottom_p3 | 1 | 2131 | Ring r1 is on the top of something, as indicated by the `clear` atom having `(top_r1)`. |
| H2 | why | state → why | yes | yes |  | 2 | 2668 | The atom `on(r1,bottom_p3)` is caused by event 4, which is `putdown_on_post[R=r1,B=bottom_p3,P=p3,V=top_r1]`. This event fires when the arm is holding ring `r1` and the post is at `bottom_p3`. |
| H3 | necessity | necessity → state → list → necessity → why | yes | yes |  | 5 | 4028 | Yes, the pickup at event 3 was necessary for ring r1 being on post p3, as event 3 is in the causal cone of event 4, which produced the state `on(r1,bottom_p3)`. |
| H4 | what-if | state → list | **no** | **no** | missing r2 | 2 | 2949 | If the arm picks up ring r1 and puts it down on post p2, the state will change. Based on the rules, the arm will become `arm_free` and the state will reflect that r1 is on p2. The next possible moves depend on the new st |
| K1 | trace | state | yes | **no** | missing b on c; missing c on a; missing a is on the table | 1 | 2217 | Block 'a' is on the table. The state also indicates that block 'b' is clear, and blocks 'b' and 'c' are on something, and 'c' and 'a' are on something. |
| K2 | why | state → why → list → graph | yes | **no** | missing pick | 4 | 3434 | Block b is on block c because event 6, which is `put_on_block[X=b,Y=c]`, fired, placing block b on block c. |
| K3 | necessity | necessity → state → list → necessity → necessity → necessity → necessity → graph | yes | yes |  | 8 (2 guarded) | 4260 | Yes, picking up `b` from the table (event 5) was necessary for `b` being on `c`. Event 5 leads to the state where `b` is picked up, which is a prerequisite for the subsequent action that places `b` on `c`. |
| K4 | necessity | sufficiency → sufficiency → state → events → why → sufficiency | yes | **no** | missing 0.5; missing 0.25 | 6 (1 guarded) | 3465 | Based on the session state, the atom `on (b,c)` is present. However, I was unable to use the `sufficiency` tool to determine the likelihood of forcing event 5 bringing about this state. |
| K5 | what-if | state → list | **no** | **no** | missing b on c | 2 | 2965 | If the arm picks up block b from the table, it will be holding block b. It can then either put block b on another clear block or put it back on the table. |

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
