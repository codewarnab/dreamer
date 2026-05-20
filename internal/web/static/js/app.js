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
    busy: false,
    msg: "",
    csrf: function () {
      const meta = document.querySelector('meta[name="csrf-token"]');
      return meta ? meta.getAttribute("content") : "";
    },
    init: function () {
      // Subscribe to SSE for status + activity.
      try {
        const es = new EventSource("/api/events");
        es.addEventListener("run.start", () => { this.status = "running"; });
        es.addEventListener("run.done", () => { this.status = "idle"; this.busy = false; this.msg = ""; });
        this._es = es;
      } catch (_) { /* SSE unsupported */ }
    },
    runNow: async function () {
      if (this.busy) return;
      this.busy = true;
      this.msg = "queuing...";
      const csrf = this.csrf();
      const m = window.location.pathname.match(/^\/projects\/([^/]+)/);
      if (m) {
        // Project page: run single project.
        try {
          const r = await fetch("/api/projects/" + encodeURIComponent(m[1]) + "/run", {
            method: "POST",
            headers: { "X-Dreamer-CSRF": csrf },
          });
          if (r.status === 409) {
            this.msg = "already running";
          } else if (r.ok) {
            this.msg = "run queued";
          } else {
            this.msg = "failed: HTTP " + r.status;
          }
        } catch (e) {
          this.msg = "failed: " + e.message;
        }
      } else {
        // Dashboard: run all projects.
        try {
          const listResp = await fetch("/api/projects");
          const data = await listResp.json();
          let queued = 0, conflict = 0;
          for (const p of (data.projects || [])) {
            const r = await fetch("/api/projects/" + encodeURIComponent(p.name) + "/run", {
              method: "POST",
              headers: { "X-Dreamer-CSRF": csrf },
            });
            if (r.ok) queued++;
            else if (r.status === 409) conflict++;
          }
          this.msg = queued + " queued" + (conflict > 0 ? ", " + conflict + " already running" : "");
        } catch (e) {
          this.msg = "failed: " + e.message;
        }
      }
      this.busy = false;
    },
  };
};
