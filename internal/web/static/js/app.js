// dreamer SPA bootstrap — HTMX CSRF wiring + Alpine root state.

(function () {
  const meta = document.querySelector('meta[name="csrf-token"]');
  const token = meta ? meta.getAttribute("content") : "";

  document.body.addEventListener("htmx:configRequest", function (evt) {
    if (evt.detail && evt.detail.headers) {
      evt.detail.headers["X-Dreamer-CSRF"] = token;
    }
  });
})();

// Alpine root state — exposed as `appState()`. Each page may extend via
// x-data="$store.<page>" but the root carries the global status pill +
// run-now action.
window.appState = function () {
  return {
    status: "running",
    csrf: function () {
      const meta = document.querySelector('meta[name="csrf-token"]');
      return meta ? meta.getAttribute("content") : "";
    },
    init: function () {
      // Subscribe to SSE for status + activity.
      try {
        const es = new EventSource("/api/events");
        es.addEventListener("run.start", () => { this.status = "running"; });
        es.addEventListener("run.done", () => { this.status = "idle"; });
        this._es = es;
      } catch (_) { /* SSE unsupported */ }
    },
    runNow: function () {
      // Determine project from path; fallback to all-projects via /api/projects iteration.
      const m = window.location.pathname.match(/^\/projects\/([^/]+)/);
      if (m) {
        fetch("/api/projects/" + encodeURIComponent(m[1]) + "/run", {
          method: "POST",
          headers: { "X-Dreamer-CSRF": this.csrf() },
        });
      } else {
        // Dashboard: fetch projects, then run each.
        fetch("/api/projects").then(r => r.json()).then(d => {
          (d.projects || []).forEach(p => {
            fetch("/api/projects/" + encodeURIComponent(p.name) + "/run", {
              method: "POST",
              headers: { "X-Dreamer-CSRF": this.csrf() },
            });
          });
        });
      }
    },
  };
};
