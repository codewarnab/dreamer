window.settingsPage = function () {
  return {
    activeSection: "assistant",
    navSections: [
      { id: "assistant", label: "assistant", meta: "" },
      { id: "scanning", label: "scanning", meta: "" },
      { id: "rules", label: "rules", meta: "" },
      { id: "safety", label: "safety", meta: "" },
      { id: "storage", label: "storage", meta: "" },
      { id: "dashboard", label: "dashboard", meta: "" },
    ],
    ruleCatalog: [
      { id: "lint-rule", label: "Lint rules", description: "Policy and style issues reported by linters." },
      { id: "test", label: "Tests", description: "Weak, missing, or theatrical test coverage." },
      { id: "ci-check", label: "CI checks", description: "Build and pipeline risks." },
      { id: "doc", label: "Documentation", description: "Missing or stale developer docs." },
      { id: "config", label: "Configuration", description: "Unsafe or confusing project configuration." },
      { id: "refactor-boundary", label: "Refactor boundaries", description: "Design seams and maintainability risks." },
    ],
    providerModelMap: {
      "copilot-sdk": ["auto"],
      "copilot-acp": ["auto"],
      "claude-cli": ["claude-haiku-4-5-20251001", "claude-sonnet-4-5-20250929", "claude-opus-4-7"],
      "claude-acp": ["claude-haiku-4-5-20251001", "claude-sonnet-4-5-20250929"],
      "gemini-cli": ["gemini-3-flash-preview", "gemini-2.5-flash", "gemini-2.5-pro"],
      "gemini-acp": ["gemini-3-flash-preview", "gemini-2.5-flash"],
      "kiro-acp": ["claude-sonnet-4-5-20250929"],
      "codex-cli": ["gpt-5.4-mini", "gpt-5.3-codex"],
      "codex-acp": ["gpt-5.4-mini"],
      "openclaude-cli": ["mimo-v2.5-pro"],
      "opencode-acp": ["deepseek-v4-flash"],
      "opencode-server": ["deepseek-v4-flash"],
      "codebuff-sdk": ["claude-opus-4-7"],
    },
    providerOptions: [],
    providersList: [],
    testString: "my password is 'supersecret123' and api_key = sk-12a3b4c",
    values: {
      assistant: { default_provider: "" },
      daemon: {},
      analyzer: { execution: {}, chunking: {}, include_subagent_transcripts: false },
      logging: {},
      redaction: { patterns: "" },
      web: {},
      sandbox: { resources: {} },
      providers: {},
      rules: {},
    },
    saved: {},
    baseline: {},
    providerConfirmed: false, // true once the user has actively chosen a provider (or one was loaded from saved config)
    revealSecret: false,
    loadError: "",
    csrf: function () {
      return document.querySelector('meta[name="csrf-token"]')?.content || "";
    },
    sectionIDs: function () {
      return this.navSections.map(s => s.id);
    },
    selectSection: function (id) {
      this.activeSection = id;
      history.replaceState(null, "", "#" + id);
    },
    cycleSection: function (dir) {
      const ids = this.sectionIDs();
      const next = ids[(ids.indexOf(this.activeSection) + dir + ids.length) % ids.length];
      this.selectSection(next);
    },
    snapshotSection: function (secID) {
      this.baseline[secID] = JSON.stringify(this.sectionBody(secID));
    },
    snapshotAll: function () {
      this.sectionIDs().forEach(id => this.snapshotSection(id));
    },
    isDirty: function (secID) {
      if (this.baseline[secID] === undefined) return false;
      return JSON.stringify(this.sectionBody(secID)) !== this.baseline[secID];
    },
    anyDirty: function () {
      return this.sectionIDs().some(id => this.isDirty(id));
    },
    // Extract the family prefix from a full provider ID by splitting on the last hyphen.
    // "claude-cli" → "claude", "opencode-acp" → "opencode", "copilot-sdk" → "copilot".
    familyOf: function (providerID) {
      const idx = providerID.lastIndexOf("-");
      return idx > 0 ? providerID.slice(0, idx) : providerID;
    },
    // Derive the ordered, deduplicated list of provider families from providerOptions.
    providerFamilies: function () {
      const seen = new Set();
      const families = [];
      for (const id of this.providerOptions) {
        const family = this.familyOf(id);
        if (!seen.has(family)) { seen.add(family); families.push(family); }
      }
      return families; // preserves providerOptions sort order
    },
    // All variant IDs that belong to a given family.
    variantsOf: function (family) {
      return this.providerOptions.filter(id => this.familyOf(id) === family);
    },
    selectProvider: function (providerID) {
      this.values.assistant.default_provider = providerID;
      this.providerConfirmed = true; // user made an explicit choice
      this.ensureProvider(providerID);
    },
    selectedProviderID: function () {
      return this.values.assistant.default_provider || "";
    },
    ensureProvider: function (providerID) {
      if (!providerID) return;
      if (!this.values.providers[providerID]) {
        this.values.providers[providerID] = {};
      }
      const block = this.values.providers[providerID];
      if (block.use_logged_in_user === undefined) block.use_logged_in_user = false;
      if (block.sandbox === undefined || block.sandbox === null) block.sandbox = "";
      if (block.model === undefined) block.model = "";
      if (block.api_key_env === undefined) block.api_key_env = "";
      if (block.password === undefined) block.password = "";
      if (block.max_input_tokens === undefined) block.max_input_tokens = "";
    },
    providerHealth: function (providerID) {
      return this.providersList.find(p => p.id === providerID) || { id: providerID, runs: 0, failures: 0, total_tokens: 0, timeouts: 0, healthy: false };
    },
    getSelectedProviderInfo: function () {
      const selected = this.selectedProviderID();
      if (!selected) return null;
      return this.providersList.find(p => p.id === selected) || null;
    },
    modelOptions: function (providerID) {
      return this.providerModelMap[providerID] || [];
    },
    resetField: function (secID, key, defVal) {
      this.values[secID][key] = defVal;
      if (secID === "assistant" && key === "default_provider") {
        this.ensureProvider(defVal);
        this.providerConfirmed = true;
      }
      if (window.Alpine) Alpine.store("toasts").add("reset " + key + " to default value", "info");
    },
    formatSeconds: function (sec) {
      if (sec === "" || sec === undefined || sec === null) return "not configured";
      const val = Number(sec);
      if (isNaN(val) || val <= 0) return "not configured";
      if (val < 60) return val + " seconds";
      if (val < 3600) {
        const m = Math.floor(val / 60);
        const s = val % 60;
        return m + " minute" + (m > 1 ? "s" : "") + (s > 0 ? " " + s + "s" : "");
      }
      const h = Math.floor(val / 3600);
      const rem = val % 3600;
      const m = Math.floor(rem / 60);
      return h + " hour" + (h > 1 ? "s" : "") + (m > 0 ? " " + m + "m" : "");
    },
    positiveNumberError: function (value, label) {
      if (value === "" || value === undefined || value === null) return "";
      const n = Number(value);
      if (isNaN(n) || n < 1) return label + " must be a positive number";
      return "";
    },
    validateFields: function (secID) {
      if (secID === "assistant") {
        const selected = this.selectedProviderID();
        if (!selected) return "Choose a default assistant";
        const tokens = this.values.providers[selected]?.max_input_tokens;
        return this.positiveNumberError(tokens, "Max input tokens");
      }
      if (secID === "scanning") {
        return this.positiveNumberError(this.values.daemon.frequency_seconds, "Scan frequency") ||
          this.positiveNumberError(this.values.daemon.max_concurrent_jobs, "Projects at once") ||
          this.positiveNumberError(this.values.analyzer.rule_timeout_seconds, "Rule timeout") ||
          this.positiveNumberError(this.values.analyzer.execution.max_concurrency, "Parallel sessions");
      }
      if (secID === "storage") {
        return this.positiveNumberError(this.values.logging.max_size_mb, "Log rotation size");
      }
      if (secID === "safety") {
        const resources = this.values.sandbox.resources || {};
        return this.positiveNumberError(resources.memory_mb, "Memory limit") ||
          this.positiveNumberError(resources.processes, "Process limit") ||
          this.positiveNumberError(resources.fds, "File handle limit") ||
          this.positiveNumberError(this.values.sandbox.sid_expiry_days, "SID cleanup days");
      }
      if (secID === "dashboard") {
        const p = Number(this.values.web.port);
        if (this.values.web.port !== "" && (isNaN(p) || p < 1 || p > 65535)) return "Port must be between 1 and 65535";
        return this.positiveNumberError(this.values.web.log_tail_kb, "Log preview size");
      }
      return "";
    },
    redactTest: function () {
      let s = this.testString || "";
      const pats = (this.values.redaction.patterns || "").split("\n").map(p => p.trim()).filter(Boolean);
      for (const p of pats) {
        try {
          let flags = "g";
          let cleanPat = p;
          if (cleanPat.startsWith("(?i)")) {
            flags += "i";
            cleanPat = cleanPat.substring(4);
          }
          s = s.replace(new RegExp(cleanPat, flags), "[REDACTED]");
        } catch (_) {}
      }
      return s;
    },
    load: async function () {
      this.loadError = "";
      const hash = location.hash.replace("#", "");
      if (this.navSections.some(s => s.id === hash)) {
        this.activeSection = hash;
      }
      window.addEventListener("beforeunload", (e) => {
        if (this.anyDirty()) {
          e.preventDefault();
          e.returnValue = "";
        }
      });
      try {
        const r = await fetch("/api/settings");
        if (!r.ok) {
          this.loadError = "failed to load settings (HTTP " + r.status + ")";
          return;
        }
        const data = await r.json();
        this.values.assistant.default_provider = data.default_provider || "";
        // If the API returned a saved provider, the detail cards can show immediately.
        if (data.default_provider) this.providerConfirmed = true;
        for (const k of ["daemon", "logging", "web"]) {
          if (data[k]) Object.assign(this.values[k], data[k]);
        }
        if (data.analyzer) {
          Object.assign(this.values.analyzer, data.analyzer);
          this.values.analyzer.execution = Object.assign({}, data.analyzer.execution || {});
          this.values.analyzer.chunking = Object.assign({}, data.analyzer.chunking || {});
          this.values.analyzer.include_subagent_transcripts = !!data.analyzer.include_subagent_transcripts;
        }
        this.values.redaction.patterns = ((data.redaction && data.redaction.patterns) || []).join("\n");
        this.values.providers = data.providers || {};
        this.values.sandbox = Object.assign({ resources: {} }, data.sandbox || {});
        this.values.sandbox.resources = Object.assign({}, (data.sandbox && data.sandbox.resources) || {});
        this.values.sandbox.project_write = !!this.values.sandbox.project_write;

        this.ruleCatalog.forEach(rule => {
          const existing = (data.analyzer && data.analyzer.rules && data.analyzer.rules[rule.id]) || {};
          this.values.rules[rule.id] = {
            enabled: existing.enabled !== undefined ? !!existing.enabled : true,
            severity: existing.severity || "",
          };
        });

        if (data.providers) {
          this.providerOptions = Object.keys(data.providers).sort();
        }
        if (!this.providerOptions.length) {
          this.providerOptions = Object.keys(this.providerModelMap).sort();
        }
        if (!this.values.assistant.default_provider && this.providerOptions.length) {
          this.values.assistant.default_provider = this.providerOptions[0];
        }
        this.providerOptions.forEach(id => this.ensureProvider(id));
        this.ensureProvider(this.values.assistant.default_provider);

        const provReq = await fetch("/api/providers");
        if (provReq.ok) {
          const provData = await provReq.json();
          this.providersList = provData.providers || provData || [];
        }
        this.snapshotAll();
      } catch (e) {
        this.loadError = "failed to fetch configuration: " + e.message;
      }
    },
    numberOrNull: function (value) {
      if (value === "" || value === undefined || value === null) return null;
      const n = Number(value);
      return isNaN(n) ? null : n;
    },
    blankToNull: function (value) {
      if (value === "" || value === undefined || value === null) return null;
      return value;
    },
    providerBody: function () {
      const providerID = this.selectedProviderID();
      this.ensureProvider(providerID);
      const provider = this.values.providers[providerID] || {};
      const block = {
        model: this.blankToNull(provider.model),
        api_key_env: this.blankToNull(provider.api_key_env),
        password: this.blankToNull(provider.password),
        max_input_tokens: this.numberOrNull(provider.max_input_tokens),
        sandbox: this.blankToNull(provider.sandbox),
      };
      if (provider.use_logged_in_user !== undefined) block.use_logged_in_user = !!provider.use_logged_in_user;
      return { default_provider: providerID || null, providers: { [providerID]: block } };
    },
    sectionBody: function (secID) {
      if (secID === "assistant") return this.providerBody();
      if (secID === "scanning") {
        return {
          daemon: {
            frequency_seconds: this.numberOrNull(this.values.daemon.frequency_seconds),
            max_concurrent_jobs: this.numberOrNull(this.values.daemon.max_concurrent_jobs),
            max_analysis_duration: this.blankToNull(this.values.daemon.max_analysis_duration),
            job_history_retention: this.blankToNull(this.values.daemon.job_history_retention),
          },
          analyzer: {
            rule_timeout_seconds: this.numberOrNull(this.values.analyzer.rule_timeout_seconds),
            include_subagent_transcripts: !!this.values.analyzer.include_subagent_transcripts,
            execution: {
              mode: this.blankToNull(this.values.analyzer.execution.mode),
              max_concurrency: this.numberOrNull(this.values.analyzer.execution.max_concurrency),
            },
            chunking: {
              max_chunk_bytes: this.numberOrNull(this.values.analyzer.chunking.max_chunk_bytes),
              provider_boundary_headroom: this.values.analyzer.chunking.provider_boundary_headroom === "" || this.values.analyzer.chunking.provider_boundary_headroom === undefined ? null : Number(this.values.analyzer.chunking.provider_boundary_headroom),
            },
          },
        };
      }
      if (secID === "rules") {
        const rules = {};
        this.ruleCatalog.forEach(rule => {
          rules[rule.id] = {
            enabled: !!this.values.rules[rule.id].enabled,
            severity: this.blankToNull(this.values.rules[rule.id].severity),
          };
        });
        return { analyzer: { rules } };
      }
      if (secID === "safety") {
        const patterns = (this.values.redaction.patterns || "").split("\n").map(s => s.trim()).filter(Boolean);
        return {
          redaction: { patterns },
          sandbox: {
            project_write: !!this.values.sandbox.project_write,
            network: this.blankToNull(this.values.sandbox.network),
            seccomp: this.blankToNull(this.values.sandbox.seccomp),
            sid_expiry_days: this.numberOrNull(this.values.sandbox.sid_expiry_days),
            resources: {
              memory_mb: this.numberOrNull(this.values.sandbox.resources.memory_mb),
              processes: this.numberOrNull(this.values.sandbox.resources.processes),
              fds: this.numberOrNull(this.values.sandbox.resources.fds),
            },
          },
        };
      }
      if (secID === "storage") {
        return {
          daemon: { output_root: this.blankToNull(this.values.daemon.output_root) },
          logging: {
            level: this.blankToNull(this.values.logging.level),
            file: this.blankToNull(this.values.logging.file),
            max_size_mb: this.numberOrNull(this.values.logging.max_size_mb),
          },
        };
      }
      if (secID === "dashboard") {
        return {
          web: {
            enabled: !!this.values.web.enabled,
            host: this.blankToNull(this.values.web.host),
            port: this.numberOrNull(this.values.web.port),
            log_tail_kb: this.numberOrNull(this.values.web.log_tail_kb),
          },
        };
      }
      return {};
    },
    save: async function (secID) {
      const errMsg = this.validateFields(secID);
      if (errMsg) {
        if (window.Alpine) Alpine.store("toasts").add(errMsg, "error");
        this.saved[secID] = "error";
        setTimeout(() => { this.saved[secID] = false; }, 4000);
        return;
      }
      try {
        const r = await fetch("/api/settings", {
          method: "PUT",
          headers: { "Content-Type": "application/json", "X-Dreamer-CSRF": this.csrf() },
          body: JSON.stringify(this.sectionBody(secID)),
        });
        this.saved[secID] = r.ok ? true : "error";
        if (r.ok) this.snapshotSection(secID);
        if (window.Alpine) Alpine.store("toasts").add(r.ok ? "settings saved" : "failed to save settings", r.ok ? "success" : "error");
      } catch (_) {
        this.saved[secID] = "error";
      }
      setTimeout(() => { this.saved[secID] = false; }, 4000);
    },
  };
};
