/* shell.js — the console's shell: the tool panels (docks) and the strip
   that toggles them, and the splitter between the page's left side and
   the docks. The metis-agent console's own copy (shell-copy s-7c739dd7);
   the metis-loop console keeps its own copy of the same file, where it
   was lifted from. Styles: shell.css.

   A classic script (no module): it sets globalThis.MetisShell, loaded by
   a plain <script> before the page's module.

     const ui = MetisShell.docks({
       names: ["claude", "catalog"],               // section.dock[data-dock] + nav#strip [data-strip]
       key: "loopconsole.docks.v2",                // the layout, per viewer (bump the version to reset it)
       defaults: {claude: {shown: true, open: true}, catalog: {shown: true, open: false}},
     });
     ui.set("catalog", {shown: true, open: true});  // show and expand one
     ui.state                                       // {name: {shown, open}}
     MetisShell.splitter();                         // #split resizes #left

   A dock's header toggles it open/collapsed, its ✕ hides it; a strip button
   shows+expands a dock, or hides it when it is shown and open. With every
   dock hidden, #right and #split hide and #left takes the width. */
(function (g) {
  "use strict";
  const store = {
    get(k, d) { try { const v = localStorage.getItem(k); return v === null ? d : JSON.parse(v); } catch (e) { return d; } },
    set(k, v) { try { localStorage.setItem(k, JSON.stringify(v)); } catch (e) { /* no storage: the layout is not kept */ } },
  };
  const $ = s => document.querySelector(s);

  function docks({ names, key, defaults = {}, store: st = store, onChange = null }) {
    const state = st.get(key, defaults) || {};
    const els = () => names.map(d => [d, $("#dock-" + d)]).filter(([, el]) => el);
    const isOpen = d => !!(state[d] && state[d].shown && state[d].open);
    function apply() {
      let any = false;
      // a dock opened without a size of its own takes the others' mean: sizes are flex-grow weights
      const sized = names.filter(d => isOpen(d) && state[d].size > 0), mean = sized.length ? sized.reduce((a, d) => a + state[d].size, 0) / sized.length : 0;
      let above = false;
      for (const [d, el] of els()) {
        const s = state[d] || { shown: false, open: true };
        el.hidden = !s.shown;
        el.classList.toggle("open", !!(s.shown && s.open));
        el.classList.toggle("collapsed", !!(s.shown && !s.open));
        el.style.flex = isOpen(d) ? `${s.size > 0 ? s.size : mean || 1} 1 0` : "";
        const g = grip(d, el);
        g.hidden = !(isOpen(d) && above);        // a grip only between two open docks
        if (isOpen(d)) above = true;
        const tw = el.querySelector(".tw");
        if (tw) tw.textContent = s.open ? "▾" : "▸";
        const b = document.querySelector(`[data-strip="${d}"]`);
        if (b) b.setAttribute("aria-pressed", !!s.shown);
        any = any || !!s.shown;
      }
      const right = $("#right"), split = $("#split"), left = $("#left");
      if (right) right.hidden = !any;
      if (split) split.hidden = !any;
      if (!any && left) left.style.flex = "1 1 auto";
      st.set(key, state);
      if (onChange) onChange(state);
    }
    function set(d, patch) { state[d] = { ...(state[d] || {}), ...patch }; apply(); }
    // the grip on a dock's top edge: drag it to move the border with the open dock above
    function grip(d, el) {
      let g = el.querySelector(":scope > .dgrip");
      if (g) return g;
      g = document.createElement("div");
      g.className = "dgrip";
      g.setAttribute("aria-hidden", "true");
      g.title = "drag to resize · double click: equal sizes";
      el.prepend(g);
      let drag = null;
      g.addEventListener("pointerdown", e => {
        const open = els().filter(([n]) => isOpen(n)), i = open.findIndex(([n]) => n === d);
        if (i < 1) return;
        e.preventDefault();
        const hs = open.map(([, x]) => x.getBoundingClientRect().height);                  // all measured before any is set
        open.forEach(([n, x], k) => { state[n].size = hs[k]; x.style.flex = `${hs[k]} 1 0`; });   // the weights in pixels now
        const [a, ea] = open[i - 1];
        drag = { a, ea, y0: e.clientY, ha: state[a].size, hb: state[d].size };
        g.classList.add("drag");
        g.setPointerCapture(e.pointerId);
      });
      g.addEventListener("pointermove", e => {
        if (!drag) return;
        const min = 90, tot = drag.ha + drag.hb, ha = Math.max(min, Math.min(tot - min, drag.ha + e.clientY - drag.y0));
        state[drag.a].size = ha; state[d].size = tot - ha;
        drag.ea.style.flex = `${ha} 1 0`; el.style.flex = `${tot - ha} 1 0`;
      });
      const end = () => { if (!drag) return; drag = null; g.classList.remove("drag"); st.set(key, state); if (onChange) onChange(state); };
      g.addEventListener("pointerup", end);
      g.addEventListener("pointercancel", end);
      g.addEventListener("dblclick", () => { for (const n of names) if (state[n]) delete state[n].size; apply(); });
      return g;
    }
    document.querySelectorAll(".dock>header").forEach(h => h.addEventListener("click", e => {
      const d = h.parentElement.dataset.dock;
      if (!names.includes(d)) return;
      if (e.target.closest(".x")) { set(d, { shown: false }); return; }
      if (e.target.closest("button,input,select,a,label")) return;    // a control in the header is its own
      set(d, { open: !(state[d] && state[d].open) });
    }));
    document.querySelectorAll("[data-strip]").forEach(b => b.addEventListener("click", () => {
      const d = b.dataset.strip, s = state[d] || {};
      if (!names.includes(d)) return;
      if (s.shown && s.open) set(d, { shown: false }); else set(d, { shown: true, open: true });
    }));
    return { state, set, apply };
  }

  // the left side's width as a share of the row (a percentage, not pixels: a window moved to another
  // monitor, or resized, keeps the proportion and the layout follows); a double click: the default
  function splitter({ split = "#split", left = "#left", min = 280 } = {}) {
    const sp = $(split), L = $(left);
    if (!sp || !L) return;
    let x0 = null, w0 = 0;
    sp.addEventListener("pointerdown", e => { x0 = e.clientX; w0 = L.getBoundingClientRect().width; sp.setPointerCapture(e.pointerId); });
    sp.addEventListener("pointermove", e => {
      if (x0 === null) return;
      const row = L.parentElement.getBoundingClientRect().width || 1;
      L.style.flex = "0 0 " + (100 * Math.max(min, w0 + e.clientX - x0) / row).toFixed(2) + "%";
    });
    sp.addEventListener("pointerup", () => { x0 = null; });
    sp.addEventListener("dblclick", () => { L.style.flex = ""; });
  }

  g.MetisShell = Object.freeze({ version: "0.3.0", store, docks, splitter });
})(globalThis);
