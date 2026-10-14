"""The dog's domain module — data, not code (e2e-workflow s-143c3088): the
card's table, the perception vocabulary, the outcomes and the alternatives
are metis-catalog domains/dog/intent.yaml; the world (pairs, words, the
answers to long commands) is dog_host.py's; the host adapter is the generic
agent_host.AgentHost. The intent pass's grammar and prompts are generated
from the same intent.yaml (metis-catalog tools/intents.py).
"""
from metis_agent.intent_domain import build

globals().update(build("dog", display="the dog", voice="own"))  # its %% lines speak for it: the narrator keeps the "I"
