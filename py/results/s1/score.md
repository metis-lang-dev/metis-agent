# S1 — the specialist on the planning catalogs

Corpus: 20 items, five per kind; wolf-goat-cabbage is seen (tuned on in step 1), bridge-torch, tower of hanoi and blocks world are clean. The kitchen pair is out: two shells on a shared env is the domain agent's job, its surprise answered in domain-agent steps 3–5.

| seat | model | items | reached | faithful | reached ∧ faithful | calls (mean) | tokens at the answer (mean) | s (mean) | cost |
|---|---|---|---|---|---|---|---|---|---|
| gemma4 | gemma-4-E2B-it-Q4_K_M.gguf | 20 | 10 | 1 | 1 | 1.3 | 2099 | 1.0 | — |
| claude | claude-opus-5-5 | 20 | 20 | 15 | 15 | 3.5 | 3552 | 18.1 | $1.33 |

## Per kind (reached / faithful)

| kind | gemma4 | claude |
|---|---|---|
| trace | 4 / 0 of 5 | 5 / 2 of 5 |
| why | 5 / 1 of 5 | 5 / 4 of 5 |
| what-if | 0 / 0 of 5 | 5 / 4 of 5 |
| necessity | 1 / 0 of 5 | 5 / 5 of 5 |
| seen (wgc) | 3 / 0 of 6 | 6 / 3 of 6 |
| clean | 7 / 1 of 14 | 14 / 12 of 14 |

## The reads

1. Verb choice: gemma4 reaches the answering instrument on 10 of 20 (bar ≥ 16): **fails**
2. Faithfulness: reached and faithful on 1 of 20 (bar ≥ 16): **fails**
3. Thinking (what-if + necessity, 10 items): off 0, cap 256 3 — the cap pays (bar: adds ≥ 3)
4. Against the baseline, faithful per kind (gemma4 − claude): trace -2, why -3, what-if -4, necessity -5

## Per item


### gemma4

| id | kind | verbs | reached | faithful | why not | calls | tokens | answer |
|---|---|---|---|---|---|---|---|---|
| W1 | trace | run | **no** | **no** | missing goat far; missing cabbage near | 1 | 2419 | The state shows that the farmer is on the far bank, the goat is on the near bank, and the wolf is on the near bank. The cabbage is on the far bank. |
| W2 | why | state → why | yes | **no** | missing wolf | 2 | 2233 | The cabbage was eaten by the goat. This is indicated by event 3, which is an `eat` event involving the cabbage, the goat, and the near/far locations. |
| W3 | what-if | — | **no** | **no** | missing goat; missing wolf | 0 | 1937 | I do not have enough information to answer your question. I need to see the model's state or trace to determine what happens in the scenario you described. |
| W4 | necessity | state → why → why → why | **no** | **no** | missing partly necessary; missing 0.83 | 4 (2 guarded) | 2455 | The cabbage is gone because it was eaten, and the state shows 'gone' was never produced. The event `carry[A=near,X=wolf,B=far]` was produced by event 1, which is related to carrying the wolf across. |
| W5 | why | state → why | yes | **no** | missing carr; missing event 1 | 2 | 2038 | The state shows the goat is on the far bank, but the `why` command indicates that the atom `goat,far` was never produced and is not in the base state. |
| W6 | trace | run | yes | **no** | missing goat is on the far | 1 | 2031 | The boat crossed the river 3 times. The goat is at the far bank. |
| B1 | what-if | — | **no** | **no** | missing 7 | 0 | 1937 | I do not have enough information to answer your question. I need to interact with the model's state or rules to determine the outcome of the crossing. |
| B2 | what-if | — | **no** | **no** | missing 12 | 0 | 1947 | I do not have access to a model or simulation that can answer questions about a physical scenario like wick burning time. I can only interact with the metis kernel model using the provided tools. |
| B3 | why | state → why | yes | yes |  | 2 | 2052 | The state shows that 'd,far' is not an atom and was never produced. Therefore, I cannot determine which crossing took 'd' to the far bank. |
| B4 | necessity | state → why → why → graph | **no** | **no** | missing 0.001 | 4 (1 guarded) | 2328 | The state shows that 'd,far' was never produced and is not in the base state. Therefore, the first crossing (c and d together) was not necessary for d ending on the far bank. |
| B5 | trace | run | yes | **no** | missing 3; missing torch is on the far; invented number(s) 1000 | 1 | 2052 | The session stopped after 1000 steps. There was 1 crossing made, and the torch is at the far bank. |
| H1 | trace | state | yes | **no** | missing bottom_p3 | 1 | 2012 | The ring r1 is currently in the `clear` state, specifically as `(top_r1)` in the `state` output. |
| H2 | why | why | yes | **no** | missing putdown_on_post; missing event 4 | 1 | 1983 | The atom `ring r1 post p3` was never produced and is not in the base state. |
| H3 | necessity | why | **no** | **no** | says 'not necessary' | 1 | 1987 | The pickup at event 3 was not necessary for ring r1 being on post p3 because p3 was never produced. |
| H4 | what-if | state | **no** | **no** | missing r1; missing r2 | 1 | 1998 | (none) |
| K1 | trace | state | yes | **no** | missing b on c; missing c on a; missing a is on the table | 1 | 1986 | The blocks stacked are `arm_free`, `clear`, and `on`. The block `on_table` is on the table. |
| K2 | why | why | yes | **no** | missing pick; missing put | 1 | 1992 | The atom `block b pred(block c)` was never produced and is not in the base state. Therefore, I cannot explain why block b is on block c based on the current state. |
| K3 | necessity | why | **no** | **no** | says 'not necessary' | 1 | 1992 | The atom 'b' was never produced and is not in the base state. Therefore, picking up 'b' (event 5) was not necessary for 'b' being on 'c'. |
| K4 | necessity | sufficiency | yes | **no** | missing 0.5; missing 0.25 | 1 | 2267 | The intervention of forcing the pickup of `b` from the table at event 5 brings about `b` being on `c` with a probability of 0, and the chance probability is also 0. |
| K5 | what-if | list | **no** | **no** | missing b on c; missing table | 1 | 2337 | (none) |

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
