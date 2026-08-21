window.jobsPage = function () {
  return {
    jobs: [],
    health: null,
    showCreateModal: false,
    form: {
      prompt: '',
      project_name: '',
      provider_id: '',
      schedule_kind: 'daily',
      time_of_day: '09:00',
      day_of_week: 'monday',
      cron: '',
      name: '',
      model: '',
      file_access: 'read_only',
      writable_paths: '',
    },
    preview: null,
    providers: [],
    projects: [],
    loading: false,
    error: '',
    showAudit: false,
    auditEvents: [],
    csrf: function () {
      return window.dreamerAPI.csrf();
    },
    _sseUnsub: [],
    load: async function () {
      const [jobsRes, settingsRes] = await Promise.all([
        fetch('/api/jobs'),
        fetch('/api/settings'),
      ]);
      if (jobsRes.ok) {
        const data = await jobsRes.json();
        this.jobs = data.jobs || [];
        this.health = data.system_health || {};
      }
      if (settingsRes.ok) {
        const cfg = await settingsRes.json();
        this.projects = (cfg.projects || []).map(function (p) { return p.name; });
        const provs = cfg.providers || {};
        this.providers = Object.keys(provs).map(function (id) {
          return { id: id, display_name: provs[id].display_name || id };
        });
        if (this.providers.length > 0 && !this.form.provider_id) {
          this.form.provider_id = this.providers[0].id;
        }
      }
      if (this._sseUnsub.length === 0) this._bindSSE();
    },
    _bindSSE: function () {
      var self = this;
      var sse = Alpine.store("sse");
      var refresh = function () { self.load(); };
      this._sseUnsub.push(sse.on("job.run.done", refresh));
      this._sseUnsub.push(sse.on("job.run.start", refresh));
      this._sseUnsub.push(sse.on("job.created", refresh));
      this._sseUnsub.push(sse.on("job.deleted", refresh));
      this._sseUnsub.push(sse.on("job.paused", refresh));
      this._sseUnsub.push(sse.on("job.resumed", refresh));
    },
    destroy: function () {
      this._sseUnsub.forEach(function (fn) { fn(); });
      this._sseUnsub = [];
    },
    openCreate: function () {
      this.error = '';
      this.preview = null;
      this.showCreateModal = true;
      this.$nextTick(function () { document.getElementById('job-prompt')?.focus(); });
    },
    closeCreate: function () {
      this.showCreateModal = false;
    },
    loadAudit: async function () {
      var r = await fetch('/api/jobs/audit');
      if (r.ok) {
        var data = await r.json();
        this.auditEvents = data.events || [];
      }
    },
    buildPayload: function () {
      return {
        prompt: this.form.prompt,
        project_name: this.form.project_name,
        provider_id: this.form.provider_id,
        schedule: window.dreamerJobs.buildSchedule(this.form),
        name: this.form.name,
        model: this.form.model,
        file_access: this.form.file_access,
        writable_paths: window.dreamerJobs.parseWritablePaths(this.form.writable_paths),
      };
    },
    previewJob: async function () {
      this.error = '';
      this.preview = null;
      this.loading = true;
      try {
        var body = this.buildPayload();
        var r = await fetch('/api/jobs/preview', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'X-Dreamer-CSRF': this.csrf() },
          body: JSON.stringify(body),
        });
        var data = await r.json();
        if (!r.ok) { this.error = data.error || 'preview failed'; return; }
        this.preview = data;
      } catch (e) { this.error = 'network error'; }
      finally { this.loading = false; }
    },
    createJob: async function () {
      this.error = '';
      this.loading = true;
      try {
        var body = this.buildPayload();
        var r = await fetch('/api/jobs', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'X-Dreamer-CSRF': this.csrf() },
          body: JSON.stringify(body),
        });
        var data = await r.json();
        if (!r.ok) { this.error = data.error || 'create failed'; return; }
        this.preview = null;
        this.form.prompt = '';
        this.form.name = '';
        this.form.model = '';
        this.form.file_access = 'read_only';
        this.form.writable_paths = '';
        this.showCreateModal = false;
        await this.load();
      } catch (e) { this.error = 'network error'; }
      finally { this.loading = false; }
    },
    pauseJob: async function (id) {
      try {
        const r = await fetch('/api/jobs/' + id + '/pause', {
          method: 'POST',
          headers: { 'X-Dreamer-CSRF': this.csrf() },
        });
        if (!r.ok) {
          this.error = 'Failed to pause job: HTTP ' + r.status;
          return;
        }
        await this.load();
      } catch (e) {
        this.error = 'Network error pausing job.';
      }
    },
    resumeJob: async function (id) {
      try {
        const r = await fetch('/api/jobs/' + id + '/resume', {
          method: 'POST',
          headers: { 'X-Dreamer-CSRF': this.csrf() },
        });
        if (!r.ok) {
          this.error = 'Failed to resume job: HTTP ' + r.status;
          return;
        }
        await this.load();
      } catch (e) {
        this.error = 'Network error resuming job.';
      }
    },
    deleteJob: async function (id) {
      if (!confirm('Delete this job?')) return;
      try {
        var r = await fetch('/api/jobs/' + encodeURIComponent(id), {
          method: 'DELETE',
          headers: { 'X-Dreamer-CSRF': this.csrf() },
        });
        if (!r.ok) {
          this.error = 'delete failed: HTTP ' + r.status;
          return;
        }
      } catch (e) {
        this.error = 'delete failed: ' + e.message;
        return;
      }
      await this.load();
    },
    runNow: async function (id) {
      try {
        const r = await fetch('/api/jobs/' + id + '/run', {
          method: 'POST',
          headers: { 'X-Dreamer-CSRF': this.csrf() },
        });
        if (!r.ok) {
          var errText = await r.text();
          var errMsg = 'run failed';
          try { errMsg = JSON.parse(errText).error || errMsg; } catch (_) {}
          this.error = errMsg;
          return;
        }
        await this.load();
      } catch (e) {
        this.error = 'Network error during run.';
      }
    },
    statusPill: function (job) {
      if (!job.enabled) return { label: 'paused', class: '' };
      switch (job.run_state) {
        case 'running': return { label: 'running', class: 'pill--info' };
        case 'never_run': return { label: 'never run', class: '' };
        case 'failed': return { label: 'last run failed', class: 'pill--warn' };
        default: return { label: 'ready', class: 'pill--healthy' };
      }
    },
  };
};
