// agentws.js — one intent turn over metis-agent's console wire (say_text → intent),
// for the parity bench (parity.mjs ws://host/ws). The intent part of fsttm-web's former
// web/backend/ws.js, copied here with the bench (fsttm-cleanup s-d7c6705a).

export class AgentWs {
  // url: ws://host/ws (node 20: node --experimental-websocket)
  constructor(url, { WebSocketImpl = globalThis.WebSocket } = {}) {
    this.ws = new WebSocketImpl(url);
    this.waiting = new Map(); // wire id → resolve(frame)
    this.sess = Math.random().toString(36).slice(2, 8); // two runs on one agent stay apart
    this.opened = new Promise((res, rej) => {
      this.ws.onopen = () => res(this);
      this.ws.onerror = () => rej(new Error(`ws ${url}: no metis-agent`));
    });
    this.ws.onmessage = (e) => {
      let f;
      try { f = JSON.parse(e.data); } catch { return; }
      const w = f.id != null && this.waiting.get(f.id);
      if (w && (f.ev === 'intent' || f.ev === 'error')) w(f);
    };
    this.ws.onclose = () => { for (const w of this.waiting.values()) w({ ev: 'error', error: 'ws closed' }); };
  }

  // intent is one intent turn whole: the JSON as the agent's pass 1 wrote it, the voice, the timings
  async intent(id, text) {
    await this.opened;
    const wid = `${this.sess}-${id}`, t0 = performance.now();
    const f = await new Promise((res) => {
      this.waiting.set(wid, res);
      this.ws.send(JSON.stringify({ ev: 'say_text', id: wid, text, mode: 'intent' }));
    });
    this.waiting.delete(wid);
    if (f.ev === 'error') throw new Error(f.error);
    const json = typeof f.intent === 'string' ? f.intent : (f.json_text ?? JSON.stringify(f.intent));
    return { json, voice: f.voice ?? '', firstMs: performance.now() - t0, frame: f };
  }

  close() { this.ws.close(); }
}
