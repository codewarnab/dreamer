window.jobDetailPage = function (jobID) {
  return {
    jobID: jobID,
    job: null,
    runs: [],
    latestRun: null,
    osSchedule: null,
    sandboxStatus: null,
    nextRuns: [],
    loading: true,
    running: false,
    error: '',
    expandedRun: null,
    activityData: null,
    activityLoading: false,
    providers: [],
    showEditModal: false,
    editLoading: false,
    editError: '',
    editForm: {
      name: '',
      prompt: '',
      provider_id: '',
      model: '',
      file_access: 'read_only',
      writable_paths: '',
      schedule_kind: 'daily',
      time_of_day: '09:00',
      day_of_week: 'monday',
      cron: '',
    },
    _sseUnsub: [],
    csrf: function () {
      return document.querySelector('meta[name="csrf-token"]')?.content || '';
    },
    load: async function () {
      this.loading = true;
      this.error = '';
      try {
        var [detailRes, runsRes, settingsRes] = await Promise.all([
          fetch('/api/jobs/' + this.jobID),
          fetch('/api/jobs/' + this.jobID + '/runs?limit=50'),
          fetch('/api/settings'),
        ]);
        if (detailRes.ok) {
          var data = await detailRes.json();
          this.job = data.job;
          this.latestRun = data.latest_run;
          this.osSchedule = data.os_schedule;
          this.nextRuns = data.next_3_runs || [];
          this.scheduleSummary = data.schedule_summary || '';
          this.sandboxStatus = data.sandbox_status || null;
        } else if (detailRes.status === 404) {
          this.error = 'Job not found.';
          return;
        } else {
          this.error = 'Failed to load job details.';
          return;
        }
        if (runsRes.ok) {
          var runsData = await runsRes.json();
          this.runs = runsData.runs || [];
        }
        if (settingsRes.ok) {
          var cfg = await settingsRes.json();
          var provs = cfg.providers || {};
          this.providers = Object.keys(provs).map(function (id) {
            return { id: id, display_name: provs[id].display_name || id };
          });
        }
        if (this._sseUnsub.length === 0) this._bindSSE();
      } catch (e) {
        this.error = 'Network error loading job.';
      } finally {
        this.loading = false;
      }
    },
    _bindSSE: function () {
      var self = this;
      var sse = Alpine.store('sse');
      var refresh = function (p) {
        if (!p || p.job_id === self.jobID) self.load();
      };
      this._sseUnsub.push(sse.on('job.run.start', refresh));
      this._sseUnsub.push(sse.on('job.run.done', refresh));
    },
    destroy: function () {
      this._sseUnsub.forEach(function (fn) { fn(); });
      this._sseUnsub = [];
    },
    pauseJob: async function () {
      await fetch('/api/jobs/' + this.jobID + '/pause', {
        method: 'POST',
        headers: { 'X-Dreamer-CSRF': this.csrf() },
      });
      await this.load();
    },
    resumeJob: async function () {
      await fetch('/api/jobs/' + this.jobID + '/resume', {
        method: 'POST',
        headers: { 'X-Dreamer-CSRF': this.csrf() },
      });
      await this.load();
    },
    deleteJob: async function () {
      if (!confirm('Delete this job permanently?')) return;
      try {
        var r = await fetch('/api/jobs/' + encodeURIComponent(this.jobID), {
          method: 'DELETE',
          headers: { 'X-Dreamer-CSRF': this.csrf() },
        });
        if (r.ok || r.status === 204) {
          window.location.href = '/jobs';
          return;
        }
        this.error = 'Failed to delete job: HTTP ' + r.status;
      } catch (e) {
        this.error = 'Failed to delete job: ' + e.message;
      }
    },
    runNow: async function () {
      this.running = true;
      this.error = '';
      try {
        var r = await fetch('/api/jobs/' + this.jobID + '/run', {
          method: 'POST',
          headers: { 'X-Dreamer-CSRF': this.csrf() },
        });
        if (!r.ok) {
          var data = await r.json();
          this.error = data.error || 'Run failed.';
          return;
        }
        await this.load();
      } catch (e) {
        this.error = 'Network error during run.';
      } finally {
        this.running = false;
      }
    },
    scheduleSummary: '',
    formatDuration: function (ms) {
      if (!ms) return '—';
      var secs = Math.floor(ms / 1000);
      if (secs < 60) return secs + 's';
      var mins = Math.floor(secs / 60);
      secs = secs % 60;
      if (mins < 60) return mins + 'm ' + secs + 's';
      var hrs = Math.floor(mins / 60);
      mins = mins % 60;
      return hrs + 'h ' + mins + 'm';
    },
    formatRunStatus: function (run) {
      if (!run) return 'never';
      var when = run.finished_at ? new Date(run.finished_at).toLocaleString() : new Date(run.started_at).toLocaleString();
      return run.status + ' ' + when;
    },
    runStatusPill: function (run) {
      switch (run.status) {
        case 'completed': return { label: 'completed', class: 'pill--healthy' };
        case 'failed': return { label: 'failed', class: 'pill--warn' };
        case 'running': return { label: 'running', class: 'pill--info' };
        case 'timed_out': return { label: 'timed out', class: 'pill--warn' };
        case 'cancelled': return { label: 'cancelled', class: '' };
        case 'skipped': return { label: 'skipped', class: '' };
        default: return { label: run.status, class: '' };
      }
    },
    toggleActivity: async function (runID) {
      if (this.expandedRun === runID) {
        this.expandedRun = null;
        this.activityData = null;
        return;
      }
      this.expandedRun = runID;
      this.activityLoading = true;
      this.activityData = null;
      try {
        var r = await fetch('/api/jobs/' + this.jobID + '/runs/' + runID + '/activity');
        if (r.ok) {
          this.activityData = await r.json();
        }
      } catch (e) {
        this.activityData = { events: [] };
      } finally {
        this.activityLoading = false;
      }
    },
    openEditModal: function () {
      if (!this.job) return;
      this.editError = '';
      this.editLoading = false;
      this.editForm.name = this.job.name || '';
      this.editForm.prompt = this.job.prompt || '';
      this.editForm.provider_id = this.job.provider_id || '';
      this.editForm.model = this.job.model || '';
      this.editForm.file_access = (this.job.permissions && this.job.permissions.file_access) || 'read_only';
      this.editForm.writable_paths = (this.job.permissions && this.job.permissions.writable_paths) ? this.job.permissions.writable_paths.join(', ') : '';
      
      var sched = this.job.schedule || {};
      this.editForm.schedule_kind = sched.kind || 'daily';
      this.editForm.time_of_day = sched.time_of_day || '09:00';
      this.editForm.day_of_week = sched.day_of_week || 'monday';
      this.editForm.cron = sched.cron || '';
      
      this.showEditModal = true;
    },
    saveJob: async function () {
      this.editError = '';
      this.editLoading = true;
      try {
        var paths = [];
        if (this.editForm.writable_paths) {
          paths = this.editForm.writable_paths.split(',').map(function (s) { return s.trim(); }).filter(Boolean);
        }
        
        var sched = { kind: this.editForm.schedule_kind, timezone: Intl.DateTimeFormat().resolvedOptions().timeZone };
        if (this.editForm.schedule_kind === 'daily') {
          sched.time_of_day = this.editForm.time_of_day;
        } else if (this.editForm.schedule_kind === 'weekly') {
          sched.time_of_day = this.editForm.time_of_day;
          sched.day_of_week = this.editForm.day_of_week;
        } else if (this.editForm.schedule_kind === 'cron') {
          sched.cron = this.editForm.cron;
        }

        var body = {
          name: this.editForm.name,
          prompt: this.editForm.prompt,
          provider_id: this.editForm.provider_id,
          model: this.editForm.model,
          file_access: this.editForm.file_access,
          writable_paths: paths,
          schedule: sched,
        };

        var r = await fetch('/api/jobs/' + encodeURIComponent(this.jobID), {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json', 'X-Dreamer-CSRF': this.csrf() },
          body: JSON.stringify(body),
        });
        
        var data = await r.json();
        if (!r.ok) {
          this.editError = data.error || 'Failed to save changes.';
          return;
        }
        
        this.showEditModal = false;
        Alpine.store('toasts').add('Job changes saved successfully', 'success');
        await this.load();
      } catch (e) {
        this.editError = 'Network error saving changes.';
      } finally {
        this.editLoading = false;
      }
    },
  };
};
