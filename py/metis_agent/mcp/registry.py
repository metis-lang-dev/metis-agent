"""The tool registry: each tool a name, a description, an input JSON schema and a handler (session, **args) -> text
or a JSON-able value. Tools register at import (tools/*.py); a group may add tools at runtime (the declared trace
tools, read off metis/trace_tools.json)."""
import json

TOOLS = {}


class ToolError(Exception):
    """A refusal in words: the tool ran and says why it did nothing (isError true, the text the reason)."""


def tool(name, description, properties=None, required=()):
    def deco(fn):
        TOOLS[name] = {"name": name, "description": description,
                       "inputSchema": {"type": "object", "properties": properties or {}, "required": list(required)},
                       "fn": fn}
        return fn
    return deco


def add(name, description, schema, fn):
    TOOLS[name] = {"name": name, "description": description, "inputSchema": schema, "fn": fn}


def listing():
    return [{k: v for k, v in t.items() if k != "fn"} for t in TOOLS.values()]


def render(value):
    if isinstance(value, str):
        return value
    return json.dumps(value, indent=1, ensure_ascii=False, default=str)


# JSON schema shorthands
def s(desc, **kw):
    return {"type": "string", "description": desc, **kw}


def i(desc, **kw):
    return {"type": "integer", "description": desc, **kw}


def b(desc):
    return {"type": "boolean", "description": desc}


def arr(desc, item=None):
    return {"type": "array", "description": desc, "items": item or {"type": "string"}}
