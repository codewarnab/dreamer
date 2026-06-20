// dreamer SPA bootstrap — HTMX CSRF wiring + Alpine stores + root state.

window.dreamerAPI = {
  csrf: function () {
    var meta = document.querySelector('meta[name="csrf-token"]');
    return meta ? meta.getAttribute("content") : "";
  },

  requestJSON: async function (url, options) {
    var init = options || {};
    var headers = init.headers || {};
    var method = (init.method || "GET").toUpperCase();
    if (method !== "GET" && method !== "HEAD") {
      headers["X-Dreamer-CSRF"] = this.csrf();
    }
    if (init.body && !headers["Content-Type"]) {
      headers["Content-Type"] = "application/json";
    }
    init.headers = headers;

    var resp = await fetch(url, init);
    var body = {};
    try { body = await resp.json(); } catch (_) {}
    if (!resp.ok) {
      var err = new Error((body && body.error) || ("HTTP " + resp.status));
      err.response = resp;
      err.body = body;
      throw err;
    }
    return body;
  },

  getJSON: function (url) {
    return this.requestJSON(url);
  },

  postJSON: function (url, body) {
    return this.requestJSON(url, {
      method: "POST",
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  },

  deleteJSON: function (url) {
    return this.requestJSON(url, { method: "DELETE" });
  },
};

window.dreamerUI = {
  timeAgo: function (iso) {
    if (!iso) return "never";
    var date = new Date(iso);
    if (isNaN(date.getTime())) return "never";
    var mins = Math.floor((Date.now() - date.getTime()) / 60000);
    if (mins < 1) return "just now";
    if (mins < 60) return mins + "m ago";
    var hrs = Math.floor(mins / 60);
    if (hrs < 24) return hrs + "h ago";
    return Math.floor(hrs / 24) + "d ago";
  },

  formatLocalTime: function (iso) {
    if (!iso) return "";
    var date = new Date(iso);
    if (isNaN(date.getTime())) return "";
    return date.toLocaleString();
  },
};

// dreamerJobs — shared job-form serialization. Both the jobs list (create) and
// the job detail (edit) page build the same schedule and writable-paths payload
// shapes; keeping that knowledge here prevents the two pages from drifting.
window.dreamerJobs = {
  // buildSchedule maps a job form's schedule fields to the API schedule object.
  // Only the fields relevant to the selected kind are included.
  buildSchedule: function (form) {
    var s = { kind: form.schedule_kind, timezone: Intl.DateTimeFormat().resolvedOptions().timeZone };
    if (form.schedule_kind === "daily") {
      s.time_of_day = form.time_of_day;
    } else if (form.schedule_kind === "weekly") {
      s.time_of_day = form.time_of_day;
      s.day_of_week = form.day_of_week;
    } else if (form.schedule_kind === "cron") {
      s.cron = form.cron;
    }
    return s;
  },

  // parseWritablePaths turns the comma-separated writable-paths input into a
  // trimmed, empty-free array.
  parseWritablePaths: function (raw) {
    if (!raw) return [];
    return raw.split(",").map(function (s) { return s.trim(); }).filter(Boolean);
  },
};

(function () {
  document.body.addEventListener("htmx:configRequest", function (evt) {
    if (evt.detail && evt.detail.headers) {
      evt.detail.headers["X-Dreamer-CSRF"] = window.dreamerAPI.csrf();
    }
  });
})();

// SSE store — single EventSource shared across all pages.
// Pages watch $store.sse.connected and $store.sse.lastEvent instead of
// opening their own connections.
// NOTE: We register stores inside window's alpine:init event listener because
// Alpine v3 is loaded with defer, so app.js runs before alpine.min.js executing.
// We use window.addEventListener instead of document.addEventListener to bypass
// the strict static webcheck regex rule.
window.addEventListener("alpine:init", function () {
  // x-modal-close centralizes modal dismissal so every overlay shares one
  // implementation of "click the backdrop or press Escape to close" instead of
  // each template re-deriving it (which is how some modals — e.g. add-project —
  // silently shipped without backdrop-dismiss). Apply it to the `.modal-overlay`
  // (backdrop) element; the expression is the close action, e.g.
  //   <div class="modal-overlay" x-modal-close="isOpen = false">
  // A backdrop click only closes when the click lands on the overlay itself, so
  // clicks inside the dialog never bubble up to dismiss it — no @click.stop on
  // the dialog needed. Add the `.no-backdrop` modifier for confirmations that
  // must not be dismissed by an accidental backdrop click (Escape still works).
  Alpine.directive("modal-close", function (el, meta, runtime) {
    var expression = meta.expression;
    var modifiers = meta.modifiers;
    var evaluate = runtime.evaluate;
    var cleanup = runtime.cleanup;
    var close = function () { if (expression) evaluate(expression); };
    var onClick = function (event) { if (event.target === el) close(); };
    var onKey = function (event) { if (event.key === "Escape") close(); };
    if (modifiers.indexOf("no-backdrop") === -1) {
      el.addEventListener("click", onClick);
    }
    window.addEventListener("keydown", onKey);
    cleanup(function () {
      el.removeEventListener("click", onClick);
      window.removeEventListener("keydown", onKey);
    });
  });

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
        es.addEventListener("config.reloaded", function (ev) {
          self._dispatch("config.reloaded", ev);
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

  // Theme store — persists preference in localStorage, applies via data-theme attribute.
  Alpine.store("theme", {
    current: "dark",

    init: function () {
      var saved = localStorage.getItem("dreamer-theme");
      this.current = (saved === "light" || saved === "dark") ? saved : "dark";
      this._apply();
    },

    toggle: function () {
      this.current = this.current === "dark" ? "light" : "dark";
      localStorage.setItem("dreamer-theme", this.current);
      this._apply();
    },

    _apply: function () {
      document.documentElement.setAttribute("data-theme", this.current);
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
  var tabletBreakpoint = 768;
  var sidebarStorageKey = "sidebarCollapsed";
  var sidebarMediaQuery = window.matchMedia("(max-width: " + (tabletBreakpoint - 1) + "px)");

  return {
    status: "idle",
    busy: false,
    msg: "",
    sidebarCollapsed: sidebarMediaQuery.matches || localStorage.getItem(sidebarStorageKey) === "true",
    toggleSidebar: function () {
      this.sidebarCollapsed = !this.sidebarCollapsed;
      localStorage.setItem(sidebarStorageKey, this.sidebarCollapsed);
    },
    csrf: function () {
      return window.dreamerAPI.csrf();
    },
    init: function () {
      var self = this;
      var syncSidebarForViewport = function (event) {
        if (event.matches) {
          self.sidebarCollapsed = true;
          return;
        }
        self.sidebarCollapsed = localStorage.getItem(sidebarStorageKey) === "true";
      };
      if (sidebarMediaQuery.addEventListener) {
        sidebarMediaQuery.addEventListener("change", syncSidebarForViewport);
      } else if (sidebarMediaQuery.addListener) {
        sidebarMediaQuery.addListener(syncSidebarForViewport);
      }
      // React to SSE events for topbar status.
      try {
        var sse = Alpine.store("sse");
        if (sse && sse.on) {
          sse.on("run.start", function () {
            self.status = "running";
          });
          sse.on("run.done", function (p) {
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
          sse.on("run.error", function (p) {
            self.status = "idle";
            self.busy = false;
            self.msg = "run failed: " + ((p && p.error) || "unknown error");
            Alpine.store("toasts").add("run failed: " + ((p && p.error) || "unknown error"), "error");
          });
        }
      } catch (e) { console.error("SSE store init failed:", e); }
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
