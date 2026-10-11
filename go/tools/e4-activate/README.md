# E4 probe — native tool call vs JSON intent for domain activation

MODEL-EXPERIMENTS.md E4: the only thing the model may "call" is a catalog
domain — `activate(domain)` / `deactivate(domain)` over the card list (or
`keep`). Two renderings of the same 12-row choice, both grammar-pinned:

    bin/llmbench -model <gemma4 gguf> -raw -prompt tools/e4-activate/prompt-toolcall.txt \
                 -gbnf tools/e4-activate/toolcall.gbnf -corpus tools/e4-activate/corpus.tsv
    bin/llmbench -model <gguf>        -raw -prompt tools/e4-activate/prompt-json.txt \
                 -gbnf tools/e4-activate/json.gbnf     -corpus tools/e4-activate/corpus.tsv

`prompt-toolcall.txt` declares the tools in Gemma 4's native `<|tool>`
format (the GGUF template's `format_function_declaration`, rendered by
hand — regenerate with the python in this directory's history if the cards
change); `toolcall.gbnf` admits exactly one `<|tool_call>call:…<tool_call|>`.
`-raw` scores the call class (verb:domain) instead of parsing a dog intent.
