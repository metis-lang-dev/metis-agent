# S1 — the specialist on the planning catalogs

Corpus: 20 items, five per kind; wolf-goat-cabbage is seen (tuned on in step 1), bridge-torch, tower of hanoi and blocks world are clean. The kitchen pair is out: two shells on a shared env is the domain agent's job, its surprise answered in domain-agent steps 3–5.

| seat | model | items | reached | faithful | reached ∧ faithful | calls (mean) | tokens at the answer (mean) | s (mean) | cost |
|---|---|---|---|---|---|---|---|---|---|
| gemma4 | gemma-4-E4B-it-Q5_K_M.gguf | 20 | 15 | 8 | 7 | 1.6 | 2300 | 1.9 | — |

## Per kind (reached / faithful)

| kind | gemma4 |
|---|---|
| trace | 4 / 2 of 5 |
| why | 5 / 3 of 5 |
| what-if | 3 / 2 of 5 |
| necessity | 3 / 1 of 5 |
| seen (wgc) | 5 / 3 of 6 |
| clean | 10 / 5 of 14 |

## The reads

1. Verb choice: gemma4 reaches the answering instrument on 15 of 20 (bar ≥ 16): **fails**
2. Faithfulness: reached and faithful on 7 of 20 (bar ≥ 16): **fails**
3. Thinking: not run (the gemma4 seat has no capped-thinking turn yet; --cap names that run)
4. Against the baseline: needs both a gemma4 and a claude run

## Per item


### gemma4

| id | kind | verbs | reached | faithful | why not | calls | tokens | answer |
|---|---|---|---|---|---|---|---|---|
| W1 | trace | — | **no** | **no** | missing farmer far; missing wolf near; missing goat far; missing cabbage near | 0 | 2072 | I do not have access to a model that describes a farmer, goat, wolf, and cabbage problem. The loaded model is `wgc.llp`, which appears to be related to a catalog, not a classic river crossing puzzle. |
| W2 | why | state → why | yes | **no** | missing wolf | 2 | 2398 | The cabbage was eaten by the goat, as indicated by event 3, which is an `eat` event. |
| W3 | what-if | state → events → do → state | yes | yes |  | 4 | 2477 | The cabbage is now on the far side, while the goat and wolf remain on the near side. No one has been eaten yet. |
| W4 | necessity | state → why → necessity | yes | **no** | missing partly necessary; missing 0.83; says 'not necessary'; says 'was necessary' | 3 | 2820 | Carrying the wolf across first was not necessary for the cabbage to be gone, as the necessity check for the eating event (event 3) showed that the event itself was necessary, not the preceding wolf-carrying event. The ne |
| W5 | why | state → why | yes | yes |  | 2 | 2385 | The goat is on the far bank because of event 1, which was a `carry` event. This event was caused by the initial state (`#init`) and the rule L38, which describes the action of carrying the goat across the river. |
| W6 | trace | run | yes | yes |  | 1 | 2148 | The boat crossed the river 3 times. At the end, the goat is on the far side. |
| B1 | what-if | state | yes | **no** | missing 7 | 1 | 2165 | The current state shows that the wick has 15 minutes left. To answer your question, I need to know the rules governing the crossing of 'c' and 'd' and how that affects the wick time. |
| B2 | what-if | — | **no** | **no** | missing 12 | 0 | 2062 | I do not have enough information to answer this question. The current model state does not contain any information about "a", "b", "torch", or "wick" to determine the remaining time. |
| B3 | why | state → why → list | yes | yes |  | 3 | 2511 | The atom `pos(d,far)` was last produced by event 1, which was `pair8[S=near,X=c,Y=d,T=far]`. This event moved `d` to the far bank, and it crossed with `c`. |
| B4 | necessity | state → events | **no** | **no** | missing not necessary; missing 0.001; says 'was necessary' | 2 | 2228 | The current state shows `pos(d,far)`, which suggests 'd ending on the far bank' is true. However, the `events` list only shows `quiescent (stage (done))`, meaning no events have fired yet to establish a causal history fo |
| B5 | trace | run | yes | **no** | missing 3; missing torch is on the far | 1 | 2168 | The session stopped after running, and the final state shows that there was 1 crossing made. The torch is at the far bank. |
| H1 | trace | state | yes | yes |  | 1 | 2108 | The ring r1 is currently on bottom_p3. |
| H2 | why | state → why | yes | yes |  | 2 | 2517 | The atom `on(r1,bottom_p3)` was last produced by event 4, which is `putdown_on_post[R=r1,B=bottom_p3,P=p3,V=top_r1]`. This event was caused by the initial state and events 1, 2, and 3. |
| H3 | necessity | state → events | **no** | **no** | missing necessary | 2 | 2269 | The events listed are 1 and 2, so there is no event 3. Please check the event numbers or provide the atom for the ring being on post. |
| H4 | what-if | state | **no** | yes |  | 1 | 2224 | The current state shows that the arm is free, and the rings are stacked as follows: r3 is on bottom_p1, r2 is on top_r3, and r1 is on top_r2. Since the arm is free, it can pick up any ring that is on top of a stack or on |
| K1 | trace | state | yes | **no** | missing b on c; missing c on a | 1 | 2100 | The blocks are stacked as follows: 'a' is on 'c', and 'c' is on 'b'. Block 'a' is on the table. |
| K2 | why | state → why | yes | **no** | missing pick | 2 | 2465 | The state shows that `on(b,c)` is the result of event 6, which was `put_on_block[X=b,Y=c]`. This event required that the arm was holding block b and that block c was clear. |
| K3 | necessity | state → necessity | yes | yes |  | 2 | 2565 | Event 5, which was picking up b from the table, was necessary for b being on c. The analysis showed that without event 5, the atom `on(b,c)` would fail with a probability of 1. |
| K4 | necessity | sufficiency | yes | **no** | missing 0.5; missing 0.25 | 1 | 2116 | The `sufficiency` tool requires an atom argument, which is the state you are checking for, not the event itself. Please provide the atom you want to check for after forcing event 5. |
| K5 | what-if | state → events | yes | **no** | missing b on c | 2 | 2200 | The arm can pick up block b from the table, or it can pick up block c from block a. If it picks up block b, the next possible action is not immediately clear from the listed events. |
