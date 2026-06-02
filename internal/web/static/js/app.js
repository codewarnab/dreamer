// dreamer SPA bootstrap — HTMX CSRF wiring + Alpine stores + root state.

(function () {
  const meta = document.querySelector('meta[name="csrf-token"]');
  const token = meta ? meta.getAttribute("content") : "";

  document.body.addEventListener("htmx:configRequest", function (evt) {
    if (evt.detail && evt.detail.headers) {
      evt.detail.headers["X-Dreamer-CSRF"] = token;
    }
  });
})();

// SSE store — single EventSource shared across all pages.
// Pages watch $store.sse.connected and $store.sse.lastEvent instead of
// opening their own connections.
document.addEventListener("alpine:init", function () {
  Alpine.store("sse", {
    connected: false,
    lastEvent: null,
    _es: null,
    _listeners: {},

    init: function () {
      this._connect();
    },

    _connect: function () {
      try {
        var self = this;
        var es = new EventSource("/api/events");

        es.onopen = function () { self.connected = true; };
        es.onerror = function () {
          self.connected = false;
          // EventSource reconnects automatically; we just track state.
        };

        es.addEventListener("run.start", function (ev) {
          self._dispatch("run.start", ev);
        });
        es.addEventListener("run.done", function (ev) {
          self._dispatch("run.done", ev);
        });
        es.addEventListener("run.error", function (ev) {
          self._dispatch("run.error", ev);
        });
        es.addEventListener("finding.applied", function (ev) {
          self._dispatch("finding.applied", ev);
        });
        es.addEventListener("finding.undone", function (ev) {
          self._dispatch("finding.undone", ev);
        });
        es.addEventListener("finding.dismissed", function (ev) {
          self._dispatch("finding.dismissed", ev);
        });
        es.addEventListener("finding.resolved", function (ev) {
          self._dispatch("finding.resolved", ev);
        });
        es.addEventListener("chat.deleted", function (ev) {
          self._dispatch("chat.deleted", ev);
        });
        es.addEventListener("job.created", function (ev) {
          self._dispatch("job.created", ev);
        });
        es.addEventListener("job.deleted", function (ev) {
          self._dispatch("job.deleted", ev);
        });
        es.addEventListener("job.run.start", function (ev) {
          self._dispatch("job.run.start", ev);
        });
        es.addEventListener("job.run.done", function (ev) {
          self._dispatch("job.run.done", ev);
        });
        es.addEventListener("job.paused", function (ev) {
          self._dispatch("job.paused", ev);
        });
        es.addEventListener("job.resumed", function (ev) {
          self._dispatch("job.resumed", ev);
        });

        this._es = es;
      } catch (e) { console.error("SSE connection failed:", e); }
    },

    _dispatch: function (type, ev) {
      var payload = null;
      try { payload = JSON.parse(ev.data); } catch (_) {}
      this.lastEvent = { type: type, payload: payload, at: new Date().toISOString() };
      // Notify registered listeners.
      var fns = this._listeners[type];
      if (fns) {
        for (var i = 0; i < fns.length; i++) {
          try { fns[i](payload); } catch (e) { console.error("SSE listener error (" + type + "):", e); }
        }
      }
    },

    // Register a listener for a specific event type. Returns an unsubscribe fn.
    on: function (type, fn) {
      if (!this._listeners[type]) this._listeners[type] = [];
      this._listeners[type].push(fn);
      var self = this;
      return function () {
        var arr = self._listeners[type];
        if (!arr) return;
        var idx = arr.indexOf(fn);
        if (idx >= 0) arr.splice(idx, 1);
      };
    },
  });

  // Toast store — non-blocking notifications.
  Alpine.store("toasts", {
    _items: [],
    _nextId: 0,

    add: function (msg, type, durationMs) {
      var id = ++this._nextId;
      this._items.push({ id: id, msg: msg, type: type || "info" });
      if (durationMs !== 0) {
        var self = this;
        setTimeout(function () { self.dismiss(id); }, durationMs || 4000);
      }
      return id;
    },

    dismiss: function (id) {
      this._items = this._items.filter(function (t) { return t.id !== id; });
    },

    get items() { return this._items; },
  });
});

// Alpine root state — exposed as `appState()`. Manages the topbar status
// pill and run-now action.
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
      var self = this;
      // React to SSE events for topbar status.
      Alpine.store("sse").on("run.start", function () {
        self.status = "running";
      });
      Alpine.store("sse").on("run.done", function (p) {
        self.status = "idle";
        self.busy = false;
        self.msg = "";
        if (p) {
          Alpine.store("toasts").add(
            "run complete: " + (p.project || "all") + " — " + (p.findings_new || 0) + " new finding(s)",
            "success"
          );
        }
      });
      Alpine.store("sse").on("run.error", function (p) {
        self.status = "idle";
        self.busy = false;
        self.msg = "run failed: " + ((p && p.error) || "unknown error");
        Alpine.store("toasts").add("run failed: " + ((p && p.error) || "unknown error"), "error");
      });
    },
    restartDaemon: async function () {
      try {
        var r = await fetch("/api/daemon/restart", {
          method: "POST",
          headers: { "X-Dreamer-CSRF": this.csrf() },
        });
        if (r.ok) {
          Alpine.store("toasts").add("daemon restart signal sent", "success");
        } else {
          Alpine.store("toasts").add("restart failed: HTTP " + r.status, "error");
        }
      } catch (e) {
        Alpine.store("toasts").add("restart failed: " + e.message, "error");
      }
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
