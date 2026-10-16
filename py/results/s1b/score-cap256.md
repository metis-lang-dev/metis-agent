# S1 — the specialist on the planning catalogs

Corpus: 20 items, five per kind; wolf-goat-cabbage is seen (tuned on in step 1), bridge-torch, tower of hanoi and blocks world are clean. The kitchen pair is out: two shells on a shared env is the domain agent's job, its surprise answered in domain-agent steps 3–5.

| seat | model | items | reached | faithful | reached ∧ faithful | calls (mean) | tokens at the answer (mean) | s (mean) | cost |
|---|---|---|---|---|---|---|---|---|---|
| gemma4 | gemma-4-E2B-it-Q4_K_M.gguf | 20 | 17 | 4 | 4 | 2.7 | 3079 | 4.9 | — |

## Per kind (reached / faithful)

| kind | gemma4 |
|---|---|
| trace | 5 / 0 of 5 |
| why | 5 / 3 of 5 |
| what-if | 3 / 0 of 5 |
| necessity | 4 / 1 of 5 |
| seen (wgc) | 6 / 1 of 6 |
| clean | 11 / 3 of 14 |

## The reads

1. Verb choice: gemma4 reaches the answering instrument on 17 of 20 (bar ≥ 16): **holds**
2. Faithfulness: reached and faithful on 4 of 20 (bar ≥ 16): **fails**
3. Thinking: not run (the gemma4 seat has no capped-thinking turn yet; --cap names that run)
4. Against the baseline: needs both a gemma4 and a claude run

## Per item


### gemma4

| id | kind | verbs | reached | faithful | why not | calls | tokens | answer |
|---|---|---|---|---|---|---|---|---|
| W1 | trace | state → list | yes | **no** | missing farmer far; missing wolf near; missing goat far; missing cabbage near | 2 | 2995 | The farmer and the goat are on the destination bank, while the wolf and the cabbage are on the starting bank. |
| W2 | why | state → why | yes | **no** | missing wolf | 2 | 2608 | The cabbage was eaten by the goat. This is indicated by the last event that produced the `gone(cabbage)` atom being the `eat` event involving the goat. |
| W3 | what-if | state → events → do → state → why → narrate | yes | **no** | missing goat; missing wolf | 6 | 4086 | Based on the trace, forcing the farmer to carry the cabbage across resulted in the cabbage being on the 'far' side. The trace does not explicitly state that anything was eaten by what, as no such event was fired in the r |
| W4 | necessity | state → why → necessity → necessity | yes | **no** | missing partly necessary; missing 0.83; says 'was necessary' | 4 | 3631 | The event that caused the cabbage to be gone was eating the cabbage (event 3), and this event was necessary for the cabbage to be gone. Carrying the wolf across was part of the sequence of events that led to the eating. |
| W5 | why | state → why → ctx | yes | yes |  | 3 | 2891 | The goat is on the far bank because of a `carry` event (event 1), which is caused by the boat, the farmer's position, and a crossing action. |
| W6 | trace | run | yes | **no** | missing 3; missing goat is on the far; invented number(s) 2 | 1 | 2409 | The boat crossed the river 2 times. The goat is at the far side. |
| B1 | what-if | state → list → run | yes | **no** | missing 7; invented number(s) 10 | 3 | 3598 | After running 10 steps, the simulation ended, and the final wick value is 15. |
| B2 | what-if | state → list | yes | **no** | missing 12 | 2 | 3620 | Based on the current state, there are 15 minutes of wick left. |
| B3 | why | state → why → events | yes | yes |  | 3 | 2994 | 'd' moved to the far bank as part of a `pair8` operation involving 'c'. |
| B4 | necessity | state → graph → necessity → necessity → necessity → why | yes | **no** | missing not necessary; missing 0.001; says 'was necessary' | 6 (1 guarded) | 3886 | Yes, the state `pos(d,far)` is produced by event 1, which is the `pair8` event representing the crossing of 'c' and 'd' at the far bank. Therefore, the first crossing was necessary for 'd' to end on the far bank. |
| B5 | trace | run | yes | **no** | missing 3; missing torch is on the far | 1 | 2372 | The session has stopped. The final state shows the torch is at the far bank. |
| H1 | trace | state | yes | **no** | missing bottom_p3 | 1 | 2260 | Based on the current state, `top_r1` is cleared, meaning ring r1 is not on the top rod. |
| H2 | why | state → why | yes | yes |  | 2 | 2799 | Ring r1 is on post p3 because it was placed there by the event `putdown_on_post[R=r1,B=bottom_p3,P=p3,V=top_r1]` (event 4). |
| H3 | necessity | state → why | **no** | **no** | says 'not necessary' | 2 | 3089 | The pickup at event 3 was not necessary for ring r1 being on post p3, as event 4, `putdown_on_post[R=r1,B=bottom_p3,P=p3,V=top_r1]`, was the last event that produced `on(r1,bottom_p3)`. |
| H4 | what-if | state → list | **no** | **no** | missing r1; missing r2 | 2 | 3093 | The arm can pick up a ring if its top is clear. Based on the current state, the arm can pick up any ring whose top is clear. |
| K1 | trace | state | yes | **no** | missing b on c; missing c on a | 1 | 2261 | Block 'a' is on the table, block 'c' is on block 'a', and block 'b' is on block 'c'. |
| K2 | why | state → why | yes | **no** | missing pick | 2 | 2743 | The relationship where block `b` is on block `c` is established by event 6, which is `put_on_block[X=b,Y=c]`. This event is described as `L36: put_on_block [story] : arm_holding(X) * clear(Y)`, indicating that the action |
| K3 | necessity | necessity → necessity → state → events → list → why → necessity | yes | **no** | missing necessary | 7 (1 guarded) | 4389 | Based on the causal cone analysis, event 6 (`put_on_block[X=b,Y=c]`) was the last event that produced the atom `on(b,c)`. Therefore, event 5 was not the direct cause for the last occurrence of `on(b,c)`. |
| K4 | necessity | state → sufficiency | yes | yes |  | 2 | 2905 | Forcing the pickup of 'b' from the table (event 5) brings about 'b on c' with a probability of 0.5, compared to a chance probability of 0.25. This means forcing the event increases the likelihood of 'b on c' by 0.25. |
| K5 | what-if | state → list | **no** | **no** | missing b on c | 2 | 2957 | If the arm picks up block 'b' from the table, it can put it down on a clear block, such as block 'c', or it can put it down on the table. |
