"""The narrator (DESIGN.md v3, "The domain agent"; RAG §11–12, E8): a
runtime fragment → one sentence in the person's words, read against the
catalog's %% comments, the script resident in the cached prefix (flat
retrieval), thinking off.

Ported from go/internal/narr (narrator.go, script.go, fragment.go) and
the speaking half of go/internal/llm SpeakCap; copied, not imported
(SPECIALIST.md §2: go/ and py/ share no code). The doctrine (Interpret),
the cue, the script entries, the fragment's body and the sentence grammar
are the Go text verbatim, so E8's numbers carry over.

    script  = the runtime's `script` op (metis.loop.cards.script), per card
    frag    = Fragment(path, seq, beat, kind, ref, counts)
    Narrator(llm, {path: script}, names).narrate(frag) → {text, inserted, ms, …}
"""
import time

INTERPRET = """You narrate, for a person, what agents are doing, one fragment at a time.
Each agent has a script: its moves, each with the catalog's comment and what it needs and leaves, and the things it counts, each with what it means.
A fragment names the agent, its kind and what it refers to — a move of its script, a change from outside, a reading, a surprise, a result or a quiet moment — and the counts after it.
Read a fragment against its script entry: the comment is what the move means, "needs" is what it takes, "leaves" is what it produces, and "now" gives the counts after it.
A move that acts on another agent changes that agent's things: say whose.
A surprise is a reading the agent did not cause: something it does not know about changed its things. Say what differs from what it expected, never that the agent did it.
Speak one short plain sentence in the person's words. Name the agent as the fragment names it. Never say a number the fragment does not carry. No markup, no names with underscores."""

CUE = "Narrate this fragment for the person in one sentence."

# a domain that speaks for itself (its %% lines are in its own voice, the
# dog's "I"; DESIGN 56da709): the fragment carries the line, bound
OWN_VOICE = """When a fragment gives the agent's line, the agent is speaking: say that line in its own words and its own voice, keeping "I" and every place and thing it names; never turn it into a sentence about the agent."""
# the cue for a fragment with its own line: the general CUE gets "The dog
# sat down." on 17 of 17 dog lines, this one first person on 17 of 17
OWN_CUE = "The agent speaks to the person. Give its line in one sentence, first person, keeping the places and things it names and the reason it gives."

# llm.go sentenceGBNF: one spoken sentence, no markup / JSON / fact syntax
SENTENCE_GBNF = 'root ::= " "? "\\""? [A-Z] [a-zA-Z0-9 ,;\'()%-]{3,200} [.!?] "\\""?\n'
SPOKEN = "Spoken response:"


# the script, the fragment and the bound line: metispy's metis.domain.fragments (moved there,
# dagent-fixtures s-86bbd1e7 step 2: the package's own words); the prompt around them is the agent's
from metis.domain.fragments import (Counts, Fragment, bind_line, distinctive, event_ref,  # noqa: F401,E402
                                    from_signal, holds_line, script_text, words)


# ── the narrator (narrator.go) + the speaking turn (llm.go SpeakCap, thinking off) ──

class Narrator:
    def __init__(self, llm, scripts, name, top_k=40, temp=0.4, max_tokens=70, seed=1, own_voice=False):
        """scripts: {path: [card, …]} as the `script` op returns them;
        name: path → display name; own_voice: the domain's lines are its
        own voice (the fragment carries the bound line; OWN_VOICE)."""
        self.llm, self.scripts, self.name = llm, scripts, name
        self.top_k, self.temp, self.max_tokens, self.seed = top_k, temp, max_tokens, seed
        self.own_voice = own_voice

    def system(self):
        return INTERPRET + ("\n" + OWN_VOICE if self.own_voice else "") + "\n\nThe scripts:\n" + "\n\n".join(
            script_text(p, cards, self.name) for p, cards in self.scripts.items())

    def fragment_text(self, f):
        return "Fragment (%s, %s): %s" % (self.name(f.path), f.kind, f.body())

    def tokens(self, text):
        return len(self.llm.tokenize(text, False))

    def narrate(self, f):
        import llama_cpp as L
        llm, sys_text = self.llm, self.system()
        user = self.fragment_text(f) + "\n\n" + (OWN_CUE if f.own_line else CUE)
        t0 = time.perf_counter()
        llm.prime(sys_text)
        llm.rewind()
        llm.decode(llm.after(llm.tail(user)))
        llm.decode(llm.tokenize(SPOKEN, False))
        ch = llm.chain(SENTENCE_GBNF, greedy=False, top_k=self.top_k, temp=self.temp, seed=self.seed)
        try:
            toks, _ = llm.generate(ch, self.max_tokens, "\n")
        finally:
            L.llama_sampler_free(ch)
        voice = llm.detokenize(toks).split("\n", 1)[0]
        text = llm.clean(voice).strip().strip('"').strip()
        return {"text": text, "inserted": user, "ms": round((time.perf_counter() - t0) * 1e3),
                "tok_fragment": self.tokens(user)}
