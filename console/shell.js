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
    function apply() {
      let any = false;
      for (const d of names) {
        const s = state[d] || { shown: false, open: true }, el = $("#dock-" + d);
        if (!el) continue;
        el.hidden = !s.shown;
        el.classList.toggle("open", !!(s.shown && s.open));
        el.classList.toggle("collapsed", !!(s.shown && !s.open));
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

  function splitter({ split = "#split", left = "#left", min = 280 } = {}) {
    const sp = $(split), L = $(left);
    if (!sp || !L) return;
    let x0 = null, w0 = 0;
    sp.addEventListener("pointerdown", e => { x0 = e.clientX; w0 = L.getBoundingClientRect().width; sp.setPointerCapture(e.pointerId); });
    sp.addEventListener("pointermove", e => { if (x0 === null) return; L.style.flex = "0 0 " + Math.max(min, w0 + e.clientX - x0) + "px"; });
    sp.addEventListener("pointerup", () => { x0 = null; });
  }

  g.MetisShell = Object.freeze({ version: "0.3.0", store, docks, splitter });
})(globalThis);
