window.dashboardPage = function () {
  return {
    stats: {},
    liveActivity: [],
    perCategory: {},
    sparkline30d: [],
    topMistakeProviders: [],
    projects: [],
    removeModal: { open: false, name: "", busy: false },
    _sseUnsub: [],

    load: async function () {
      var self = this;
      var results = await Promise.all([
        window.dreamerAPI.getJSON("/api/dashboard").catch(function () { return {}; }),
        window.dreamerAPI.getJSON("/api/projects").catch(function () { return {}; }),
      ]);
      var data = results[0];
      var projectData = results[1];
      self.stats = data.stats || {};
      self.liveActivity = data.live_activity || [];
      self.perCategory = data.per_category || {};
      self.sparkline30d = data.sparkline_30d || [];
      self.topMistakeProviders = data.top_mistake_providers || [];
      self.projects = projectData.projects || [];
      if (self._sseUnsub.length === 0) self._bindSSE();
    },

    timeAgo: function (iso) {
      return window.dreamerUI.timeAgo(iso);
    },

    formatLocalTime: function (iso) {
      return window.dreamerUI.formatLocalTime(iso);
    },

    nextRunText: function () {
      if (!this.stats.next_run_utc) return "";
      var date = new Date(this.stats.next_run_utc);
      if (isNaN(date.getTime())) return "";
      var diffMs = date.getTime() - Date.now();
      if (diffMs <= 0) return "due now";
      var mins = Math.ceil(diffMs / 60000);
      if (mins < 60) return "next run in " + mins + "m";
      return "next run in " + Math.ceil(mins / 60) + "h";
    },

    _bindSSE: function () {
      var self = this;
      var sse = Alpine.store("sse");
      var refresh = function () { self.load(); };
      this._sseUnsub.push(sse.on("run.done", refresh));
      this._sseUnsub.push(sse.on("run.start", refresh));
      this._sseUnsub.push(sse.on("finding.applied", refresh));
      this._sseUnsub.push(sse.on("finding.dismissed", refresh));
      this._sseUnsub.push(sse.on("finding.resolved", refresh));
      this._sseUnsub.push(sse.on("config.reloaded", refresh));
    },

    destroy: function () {
      this._sseUnsub.forEach(function (fn) { fn(); });
      this._sseUnsub = [];
    },

    confirmRemove: function (name) {
      this.removeModal.name = name;
      this.removeModal.busy = false;
      this.removeModal.open = true;
    },

    doRemove: async function () {
      var self = this;
      if (self.removeModal.busy) return;
      self.removeModal.busy = true;
      var name = self.removeModal.name;
      try {
        await window.dreamerAPI.deleteJSON("/api/projects/" + encodeURIComponent(name));
        self.removeModal.open = false;
        Alpine.store("toasts").add("project " + name + " removed", "success");
        self.projects = self.projects.filter(function (project) { return project.name !== name; });
      } catch (err) {
        Alpine.store("toasts").add("remove failed: " + err.message, "error");
      }
      self.removeModal.busy = false;
    },

    sparklinePoints: function () {
      var pts = this.sparkline30d;
      if (!pts || pts.length === 0) return "";
      var vals = pts.map(function (day) { return day.findings_new || 0; });
      var lo = Math.min.apply(null, vals);
      var hi = Math.max.apply(null, vals);
      var range = hi - lo || 1;
      var w = 300;
      var h = 60;
      var pad = 4;
      var step = w / Math.max(pts.length - 1, 1);
      return vals.map(function (value, i) {
        return (i * step).toFixed(1) + "," + (pad + (1 - (value - lo) / range) * (h - 2 * pad)).toFixed(1);
      }).join(" ");
    },
  };
};
