#!/usr/bin/env python3
"""E10b question 1 (docs/MODEL-EXPERIMENTS.md "E10b"): is T-api's hand
rendering byte-identical to what the GGUF chat template renders?

The template is read from the model (tokenizer.chat_template; E2B and E4B
carry the same one) and rendered with jinja2 on the tools as JSON in the
OpenAI function shape, the way llama.cpp --jinja hands them over:

  dog      ../../metis-catalog/domains/dog/intent/bench/dog-tools.json, $defs expanded (the template does not
           resolve $ref)
  decider  kernel.NewDecider's three, built by hand in decide.go: the same
           fields as JSON here
  activate tools/e4-activate/prompt-toolcall.txt's three, the same

Three comparisons, each reported item by item:

  1. the system turn: bos + <|turn>system\\n + the E10 T-api system text +
     the declarations + <turn|>\\n, the bench's (renderGemma4 on
     grammar/e10-prompt-t.txt) against the template's
  2. every declaration, one by one, and the parameterless tools also in
     the OpenAI form {"type":"object","properties":{}}
  3. the turn shapes the E10 corpus injects (sync ack, async outcome in a
     later model turn, after a user turn, an OpenAI role:tool response)

With a difference in 1 it writes grammar/e10-prompt-t-tmpl.txt: the system
text exactly as the template renders it, so llmbench's renderGemma4 on that
file gives the template's system turn byte for byte.

    python3 tools/e10b-template.py [--model PATH]      # from go/
"""
import argparse
import difflib
import importlib.util
import json
import os
import sys
from pathlib import Path

import jinja2

GO = Path(__file__).resolve().parent.parent
LLAMA = Path(os.environ.get("LLAMA_DIR", Path.home() / "archive/vox/llama.cpp"))
Q = '<|"|>'


def _load(name):
    spec = importlib.util.spec_from_file_location(name.replace("-", "_"), GO / "tools" / (name + ".py"))
    m = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(m)
    return m


def chat_template(model):
    sys.path.insert(0, str(LLAMA / "gguf-py"))
    from gguf import GGUFReader
    f = GGUFReader(model).fields["tokenizer.chat_template"]
    return bytes(f.parts[f.data[0]]).decode()


def fn(name, description, parameters=None):
    f = {"name": name, "description": description}
    if parameters is not None:
        f["parameters"] = parameters
    return {"type": "function", "function": f}


def enum_param(description, values):
    return {"type": "STRING", "description": description, "enum": values}


def tools_json(empty_params=False):
    """The T-api tool list in the bench's order: dog, decider, activation.
    empty_params: the parameterless tools carry {"type":"object",
    "properties":{}}, as OpenAI-style clients send them."""
    e9 = _load("e9-tools")
    none = {"type": "object", "properties": {}} if empty_params else None
    out = [fn(t["name"], t["description"], t.get("parameters", none)) for t in e9.load_tools()]
    out += [
        fn("reset", "the reading was noise: set it aside and resume on the model", none),
        fn("explain", "a sibling card's move explains the reading", {
            "type": "OBJECT",
            "properties": {"card": enum_param("the sibling card", ["kitchen_shared"]),
                           "move": enum_param("the card's move that produced the difference", ["partner_load"])},
            "required": ["card", "move"]}),
        fn("escalate", "nothing explains the reading: describe it for the revision loop", {
            "type": "OBJECT",
            "properties": {"text": {"type": "STRING", "description": "one plain sentence: what was seen"}},
            "required": ["text"]}),
    ]
    domains = ["kitchen", "nav", "xtraffic", "sysadmin", "social", "votes"]
    for verb, desc in (("activate", "attach a catalog domain to the current runtime tree because the outside context now needs it"),
                       ("deactivate", "detach a catalog domain from the runtime tree because the outside context no longer needs it")):
        out.append(fn(verb, desc, {"type": "OBJECT",
                                   "properties": {"domain": enum_param("the catalog domain (card) to %s" % verb, domains)},
                                   "required": ["domain"]}))
    out.append(fn("keep", "change nothing: the context change needs no domain attached or detached", none))
    return out


class Tmpl:
    def __init__(self, src):
        env = jinja2.Environment(extensions=["jinja2.ext.loopcontrols"])
        env.filters.setdefault("tojson", json.dumps)
        self.t = env.from_string(src)

    def render(self, messages, tools=None, gen=False):
        return self.t.render(messages=messages, tools=tools, bos_token="<bos>", add_generation_prompt=gen)


def decls(s):
    """The <|tool>…<tool|> blocks of a rendering, by tool name."""
    out = {}
    for part in s.split("<|tool>")[1:]:
        body = part[:part.index("<tool|>")]
        out[body[len("declaration:"):body.index("{")]] = body
    return out


def first_diff(a, b):
    i = next((k for k in range(min(len(a), len(b))) if a[k] != b[k]), min(len(a), len(b)))
    return i, a[max(0, i - 40):i + 60], b[max(0, i - 40):i + 60]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--model", default=str(GO / "models/gemma4/gemma-4-E2B-it-Q4_K_M.gguf"))
    ap.add_argument("--prompt", default=str(GO / "grammar/e10-prompt-t.txt"))
    ap.add_argument("--out", default=str(GO / "grammar/e10-prompt-t-tmpl.txt"))
    ap.add_argument("--dump", default="", help="also write the two system turns and the tools JSON into this directory")
    a = ap.parse_args()

    tm = Tmpl(chat_template(a.model))
    prompt = Path(a.prompt).read_text()
    cut = prompt.index("<|tool>")
    text, hand = prompt[:cut], prompt[cut:]
    tools = tools_json()
    bench = "<bos><|turn>system\n" + prompt + "<turn|>\n"  # llm.renderGemma4 + the tokenizer's BOS
    tmpl = tm.render([{"role": "system", "content": text}], tools)
    findings = []

    print("== 1. the system turn")
    print("bench %d bytes, template %d bytes" % (len(bench), len(tmpl)))
    if bench == tmpl:
        print("byte-identical")
    else:
        i, x, y = first_diff(bench, tmpl)
        print("first difference at byte %d\n  bench:    %r\n  template: %r" % (i, x, y))
        sm = difflib.SequenceMatcher(None, bench, tmpl, autojunk=False)
        for op, i1, i2, j1, j2 in sm.get_opcodes():
            if op != "equal":
                print("  %s bench[%d:%d]=%r  template[%d:%d]=%r" % (op, i1, i2, bench[i1:i2], j1, j2, tmpl[j1:j2]))
                findings.append(("system turn", op, bench[i1:i2], tmpl[j1:j2]))

    print("\n== 2. the declarations, one by one")
    hd, td = decls(hand), decls(tmpl)
    print("order: bench %s\n       template %s  (%s)" % (
        list(hd), list(td), "same" if list(hd) == list(td) else "DIFFERENT"))
    for name in hd:
        if hd[name] == td.get(name):
            print("  %-12s identical (%d bytes)" % (name, len(hd[name])))
        else:
            i, x, y = first_diff(hd[name], td.get(name, ""))
            print("  %-12s DIFFERS at %d\n     bench:    %r\n     template: %r" % (name, i, x, y))
            findings.append(("declaration " + name, "replace", hd[name], td.get(name)))
    alt = decls(tm.render([{"role": "system", "content": text}], tools_json(empty_params=True)))
    print("  parameterless tools sent as {type:object,properties:{}} (the OpenAI client form):")
    for name in [n for n in hd if "parameters:" not in hd[n]]:
        print("    %-10s %r" % (name, alt[name][alt[name].index("{description"):][-60:]))

    print("\n== 3. the turn shapes")
    call = {"type": "function", "function": {"name": "LOCAL_ACTION", "arguments": {"action": "STAND_UP"}}}
    acc = {"name": "LOCAL_ACTION", "response": {"id": "c1", "status": "accepted"}}
    done = {"name": "LOCAL_ACTION", "response": {"id": "c1", "status": "done"}}
    sysm = {"role": "system", "content": "S"}
    head = tm.render([sysm])
    shapes = [
        ("A/B sync ack: call + accepted + the line in one model turn",
         [{"role": "user", "content": "stand up"},
          {"role": "assistant", "tool_calls": [call], "tool_responses": [acc], "content": "Okay."}],
         "<|turn>user\nstand up<turn|>\n<|turn>model\n<|tool_call>call:LOCAL_ACTION{action:<|\"|>STAND_UP<|\"|>}<tool_call|>"
         "<|tool_response>response:LOCAL_ACTION{id:<|\"|>c1<|\"|>,status:<|\"|>accepted<|\"|>}<tool_response|>Okay.<turn|>\n"),
        ("B async outcome: the next message is the model's again (the corpus opens a new model turn)",
         [{"role": "user", "content": "stand up"},
          {"role": "assistant", "tool_calls": [call], "tool_responses": [acc], "content": "Okay."},
          {"role": "assistant", "tool_responses": [done], "content": "I'm up."}],
         "<|turn>user\nstand up<turn|>\n<|turn>model\n<|tool_call>call:LOCAL_ACTION{action:<|\"|>STAND_UP<|\"|>}<tool_call|>"
         "<|tool_response>response:LOCAL_ACTION{id:<|\"|>c1<|\"|>,status:<|\"|>accepted<|\"|>}<tool_response|>Okay.<turn|>\n"
         "<|turn>model\n<|tool_response>response:LOCAL_ACTION{id:<|\"|>c1<|\"|>,status:<|\"|>done<|\"|>}<tool_response|>I'm up.<turn|>\n"),
        ("F async outcome after a user turn",
         [{"role": "user", "content": "stand up"},
          {"role": "assistant", "tool_calls": [call], "tool_responses": [acc], "content": "Okay."},
          {"role": "user", "content": "x"},
          {"role": "assistant", "tool_responses": [done], "content": "I'm up."}],
         "<|turn>user\nstand up<turn|>\n<|turn>model\n<|tool_call>call:LOCAL_ACTION{action:<|\"|>STAND_UP<|\"|>}<tool_call|>"
         "<|tool_response>response:LOCAL_ACTION{id:<|\"|>c1<|\"|>,status:<|\"|>accepted<|\"|>}<tool_response|>Okay.<turn|>\n"
         "<|turn>user\nx<turn|>\n"
         "<|turn>model\n<|tool_response>response:LOCAL_ACTION{id:<|\"|>c1<|\"|>,status:<|\"|>done<|\"|>}<tool_response|>I'm up.<turn|>\n"),
        ("OpenAI role:tool message, JSON string content (llama-server's /v1/chat/completions)",
         [{"role": "user", "content": "stand up"},
          {"role": "assistant", "tool_calls": [dict(call, id="c1")], "content": ""},
          {"role": "tool", "tool_call_id": "c1", "content": json.dumps({"id": "c1", "status": "accepted"})},
          {"role": "assistant", "content": "Okay."}],
         "<|turn>user\nstand up<turn|>\n<|turn>model\n<|tool_call>call:LOCAL_ACTION{action:<|\"|>STAND_UP<|\"|>}<tool_call|>"
         "<|tool_response>response:LOCAL_ACTION{id:<|\"|>c1<|\"|>,status:<|\"|>accepted<|\"|>}<tool_response|>Okay.<turn|>\n"),
        ("the generation prompt after a bare call (where the bench injects the response)",
         [{"role": "user", "content": "stand up"}, {"role": "assistant", "tool_calls": [call]}],
         "<|turn>user\nstand up<turn|>\n<|turn>model\n<|tool_call>call:LOCAL_ACTION{action:<|\"|>STAND_UP<|\"|>}<tool_call|><|tool_response>"),
    ]
    for label, msgs, want in shapes:
        got = tm.render([sysm] + msgs, gen=label.startswith("the generation"))[len(head):]
        ok = got == want
        print("  %s: %s" % ("same" if ok else "DIFFERS", label))
        if not ok:
            print("     corpus:   %r\n     template: %r" % (want, got))
            findings.append(("turn: " + label, "replace", want, got))

    if bench != tmpl:
        body = tmpl[len("<bos><|turn>system\n"):-len("<turn|>\n")]
        Path(a.out).write_text(body)
        print("\nwrote %s (%d bytes): the template's system text; renderGemma4 on it = the template's turn: %s"
              % (os.path.relpath(a.out, GO), len(body), "<bos><|turn>system\n" + body + "<turn|>\n" == tmpl))
        # E10b's corpus: groups A and B of both arms as in run1, and arm
        # T-tmpl = T-api's items on the template's prompt
        n, out = 0, []
        for ln in (GO / "grammar/e10-corpus.jsonl").read_text().splitlines():
            it = json.loads(ln)
            if it["group"] not in ("A", "B"):
                continue
            out.append(ln)
            if it["arm"] == "T-api":
                it.update(arm="T-tmpl", prompt=os.path.relpath(a.out, GO))
                out.append(json.dumps(it, ensure_ascii=False))
                n += 1
        (GO / "grammar/e10b-corpus.jsonl").write_text("\n".join(out) + "\n")
        print("wrote grammar/e10b-corpus.jsonl: %d items, %d of them T-tmpl" % (len(out), n))
    if a.dump:
        d = Path(a.dump)
        d.mkdir(parents=True, exist_ok=True)
        (d / "system-bench.txt").write_text(bench)
        (d / "system-template.txt").write_text(tmpl)
        (d / "tools.json").write_text(json.dumps(tools, indent=1, ensure_ascii=False))
    print("\n%d difference(s)" % len(findings))
    return 0


if __name__ == "__main__":
    sys.exit(main())
