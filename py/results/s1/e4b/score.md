# S1 — the specialist on the planning catalogs

Corpus: 20 items, five per kind; wolf-goat-cabbage is seen (tuned on in step 1), bridge-torch, tower of hanoi and blocks world are clean. The kitchen pair is out: two shells on a shared env is the domain agent's job, its surprise answered in domain-agent steps 3–5.

| seat | model | items | reached | faithful | reached ∧ faithful | calls (mean) | tokens at the answer (mean) | s (mean) | cost |
|---|---|---|---|---|---|---|---|---|---|
| gemma4 | gemma-4-E4B-it-Q5_K_M.gguf | 20 | 16 | 8 | 8 | 2.2 | 2248 | 2.6 | — |
| claude | claude-opus-5-5 | 20 | 20 | 15 | 15 | 3.5 | 3552 | 18.1 | $1.33 |

## Per kind (reached / faithful)

| kind | gemma4 | claude |
|---|---|---|
| trace | 4 / 2 of 5 | 5 / 2 of 5 |
| why | 4 / 2 of 5 | 5 / 4 of 5 |
| what-if | 4 / 3 of 5 | 5 / 4 of 5 |
| necessity | 4 / 1 of 5 | 5 / 5 of 5 |
| seen (wgc) | 4 / 2 of 6 | 6 / 3 of 6 |
| clean | 12 / 6 of 14 | 14 / 12 of 14 |

## The reads

1. Verb choice: gemma4 reaches the answering instrument on 16 of 20 (bar ≥ 16): **holds**
2. Faithfulness: reached and faithful on 8 of 20 (bar ≥ 16): **fails**
3. Thinking (what-if + necessity, 10 items): off 4, cap 256 4 — the cap does not pay (bar: adds ≥ 3)
4. Against the baseline, faithful per kind (gemma4 − claude): trace +0, why -2, what-if -1, necessity -4

## Per item


### gemma4

| id | kind | verbs | reached | faithful | why not | calls | tokens | answer |
|---|---|---|---|---|---|---|---|---|
| W1 | trace | — | **no** | **no** | missing farmer far; missing wolf near; missing goat far; missing cabbage near | 0 | 1953 | I do not have access to a model that describes a farmer, goat, wolf, and cabbage problem. The loaded model is `wgc.llp`, which appears to be related to a different domain. |
| W2 | why | state → why | yes | **no** | missing wolf | 2 | 2305 | The cabbage was eaten by the goat, as shown by event 3: `eat[Y=cabbage,A=near,X=goat,B=far]`. |
| W3 | what-if | state → events → do → run | yes | yes |  | 4 | 2391 | After running 3 steps, the state shows that the goat has been eaten by the wolf. |
| W4 | necessity | state → events → why → necessity | yes | **no** | missing partly necessary; missing 0.83; says 'not necessary' | 4 | 2692 | The necessity check indicates that event 2 was not necessary for event 3 to occur. Therefore, carrying the wolf across first was not necessary for the cabbage to be gone. |
| W5 | why | state → events | **no** | **no** | missing carr; missing event 1 | 2 | 2118 | The current state shows the goat is on the far bank, as indicated by `(goat,far)`. The events list shows two possible transitions, but neither explicitly explains *why* the goat is there. |
| W6 | trace | run | yes | yes |  | 1 | 2035 | The boat crossed the river 3 times. At the end, the goat is on the far side. |
| B1 | what-if | state → events → step → state | yes | yes |  | 4 | 2714 | After c and d cross the bridge together, 7 minutes of wick are left. |
| B2 | what-if | — | **no** | **no** | missing 12 | 0 | 1959 | I do not have enough information to answer this question. The model is loaded with a bridge catalog and its state is described in terms of atoms and tokens, not a scenario involving "a", "b", "torch", or "minutes of wick |
| B3 | why | state → list → why | yes | yes |  | 3 | 2404 | The position of 'd' on the far bank was established by event 1, which was `pair8[S=near,X=c,Y=d,T=far]`. This crossing involved 'c' and 'd', with 'c' starting near and 'd' ending far. |
| B4 | necessity | state → events | **no** | **no** | missing not necessary; missing 0.001 | 2 | 2123 | The current state shows that the stage is `done` and atom `(d,far)` is present. However, there are no events listed, and I don't have enough information to identify the "first crossing (c and d together)" or to perform a |
| B5 | trace | run | yes | **no** | missing 3; missing torch is on the far | 1 | 2054 | The session stopped after running, and the final state shows that 1 crossing was made. The torch is at the far bank. |
| H1 | trace | state | yes | yes |  | 1 | 1997 | The ring r1 is currently on top of bottom_p3. |
| H2 | why | state → why | yes | yes |  | 2 | 2382 | The ring r1 is on post p3 because of event 4, which was a `putdown_on_post` action. This event was caused by the preceding events 1, 2, and 3, which involved picking up and placing rings. |
| H3 | necessity | state → events → necessity | yes | **no** | says 'not necessary' | 3 | 2209 | The pickup event at event 3 was not necessary for ring r1 being on post p3 because the state (r1,bottom_p3) did not hold in the fact at the readout after event 2. |
| H4 | what-if | state → events | yes | yes |  | 2 | 2157 | The only enabled event is `pickup[T=top_r1,R=r1,P=top_r2]`, which means the arm can only pick up ring r1 from top_r2. |
| K1 | trace | state | yes | **no** | missing b on c; missing c on a; missing a is on the table | 1 | 2001 | The current state shows that block 'a' is on the table. The blocks are stacked such that 'b' is clear, 'c' is on 'a', and 'a' is on the table. |
| K2 | why | state → why | yes | **no** | missing pick | 2 | 2350 | The state shows that block b is on block c because of event 6, which was a `put_on_block[X=b,Y=c]`. This event was caused by the preceding events in the causal cone. |
| K3 | necessity | events → state → why → necessity | yes | yes |  | 4 | 2756 | Event 5, picking up b from the table, was necessary for b being on c. The necessity calculation showed that without event 5 firing, the probability of b being on c was 0. |
| K4 | necessity | sufficiency | yes | **no** | missing 0.5; missing 0.25 | 1 | 1998 | The `sufficiency` tool requires an atom to check for the effect of forcing an event. Please specify the atom you are interested in, such as `b on c`. |
| K5 | what-if | state → events → step → state → events | yes | **no** | missing b on c | 5 | 2357 | If the arm picks up block b from the table, it can next put b on the table or put b on block c. |

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
