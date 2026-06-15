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
    // providerModelMap is the fallback model list used when /api/provider-meta
    // is unavailable (network error, old server). Kept in sync manually as a
    // last-resort; the live endpoint is authoritative.
    providerModelMap: {
      "copilot-sdk": ["auto"],
      "copilot-acp": ["auto"],
      "claude-cli": ["claude-haiku-4-5-20251001", "claude-sonnet-4-5-20250929", "claude-sonnet-4-6", "claude-opus-4-6"],
      "claude-acp": ["claude-haiku-4-5-20251001", "claude-sonnet-4-5-20250929", "claude-sonnet-4-6"],
      "gemini-cli": ["gemini-3-flash-preview", "gemini-3-pro-preview", "gemini-3.1-pro-preview", "gemini-3.1-flash-lite", "gemini-3.5-flash", "gemini-2.5-flash", "gemini-2.5-flash-lite", "gemini-2.5-pro"],
      "gemini-acp": ["gemini-3-flash-preview", "gemini-3-pro-preview", "gemini-2.5-flash"],
      "kiro-acp": ["claude-sonnet-4.5", "claude-sonnet-4"],
      "codex-cli": ["gpt-5.4-mini", "gpt-5.3-codex"],
      "codex-acp": ["gpt-5.4-mini"],
      "openclaude-cli": ["mimo-v2.5-pro"],
      "opencode-acp": ["deepseek-v4-flash"],
      "opencode-server": ["deepseek-v4-flash"],
      "codebuff-sdk": ["claude-opus-4-6"],
    },
    // providerMetaMap is populated from /api/provider-meta on load. Holds the
    // full metadata entry (display_name, models, default_model, remediation)
    // keyed by provider id.
    providerMetaMap: {},
    providerOptions: [],
    providersList: [],
    providerTestRunning: false,
    providerTestResult: null,   // { ok, latency_ms } | { ok, error, category }
    // modelFamilyApiKeyEnv maps model-name prefixes to the env var that carries
    // the API key for that model family. Ordered most-specific first so prefix
    // matching short-circuits early.
    modelFamilyApiKeyEnv: [
      { prefix: "claude-",    env: "ANTHROPIC_API_KEY" },
      { prefix: "gpt-",       env: "OPENAI_API_KEY"    },
      { prefix: "o1",         env: "OPENAI_API_KEY"    },
      { prefix: "o3",         env: "OPENAI_API_KEY"    },
      { prefix: "gemini-",    env: "GEMINI_API_KEY"    },
      { prefix: "mistral-",   env: "MISTRAL_API_KEY"   },
      { prefix: "ministral-", env: "MISTRAL_API_KEY"   },
      { prefix: "grok-",      env: "XAI_API_KEY"       },
      { prefix: "mimo-",      env: "OPENAI_API_KEY"    },
      { prefix: "deepseek-",  env: "OPENAI_API_KEY"    },
      { prefix: "llama",      env: "OPENAI_API_KEY"    },
    ],
    // errorCategories maps regex patterns to { label, action } pairs for
    // the categorised error display in the health card. Ordered most-specific
    // first. Mirrors providerErrorCategory() in handlers/providers.go —
    // both must be updated together when new patterns are added.
    errorCategories: [
      { pattern: /rate.?limit|429|quota.?exceeded/i,                              label: "rate limited",    action: "Wait a few minutes, or switch to a different model."             },
      { pattern: /not.?found.?in.?PATH|binary.*not found|not installed/i,         label: "not installed",   action: "Run the remediation command shown above, then retry."             },
      { pattern: /all \d+ output lines failed to parse|provider schema change/i,  label: "version mismatch",action: "Update the provider CLI: npm update -g @gitlawb/openclaude"      },
      { pattern: /permission denied|auth|unauthori[zs]ed|invalid.*key|api key/i,  label: "auth failure",    action: "Check the API key variable or direct key above."                  },
      { pattern: /timeout|deadline|context canceled/i,                            label: "timed out",       action: "Increase max_analysis_duration or reduce transcript size."         },
      { pattern: /no assistant content/i,                                          label: "empty response",  action: "The model returned nothing. Check the model name is valid."       },
    ],
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
      this.providerTestResult = null; // clear stale test result from prior provider
      this.autofillApiKeyEnv();
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
      if (block.max_turns === undefined) block.max_turns = "";
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
      // Prefer live data from /api/provider-meta; fall back to hardcoded map.
      if (this.providerMetaMap[providerID] && this.providerMetaMap[providerID].models) {
        return this.providerMetaMap[providerID].models;
      }
      return this.providerModelMap[providerID] || [];
    },
    // loadProviderMeta fetches /api/provider-meta and replaces the hardcoded
    // providerModelMap + providerMetaMap with live server data. Falls back
    // gracefully to the hardcoded map on network errors or old servers.
    loadProviderMeta: async function () {
      try {
        const r = await fetch("/api/provider-meta");
        if (!r.ok) return;
        const data = await r.json();
        for (const p of (data.providers || [])) {
          if (p.models && p.models.length > 0) {
            this.providerModelMap[p.id] = p.models;
          }
          this.providerMetaMap[p.id] = p;
        }
      } catch (_) {
        // Graceful degradation: retain hardcoded fallback map.
      }
    },
    // apiKeyEnvForModel returns the canonical env var name for the API key
    // required by the given model, based on prefix matching.
    apiKeyEnvForModel: function (model) {
      if (!model) return "";
      const lower = model.toLowerCase();
      for (const entry of this.modelFamilyApiKeyEnv) {
        if (lower.startsWith(entry.prefix)) return entry.env;
      }
      return "";
    },
    // autofillApiKeyEnv suggests an api_key_env value based on the current
    // model name. Never overwrites an existing user value.
    autofillApiKeyEnv: function () {
      const providerID = this.selectedProviderID();
      const block = this.values.providers[providerID] || {};
      if (block.api_key_env) return; // never clobber an explicit value
      const suggested = this.apiKeyEnvForModel(block.model);
      if (suggested) block.api_key_env = suggested;
    },
    // setModel sets the model for the currently selected provider and
    // triggers API key autofill. Used by the quick-pick chip strip.
    setModel: function (model) {
      const providerID = this.selectedProviderID();
      if (!providerID) return;
      this.values.providers[providerID].model = model;
      this.autofillApiKeyEnv();
    },
    // categoriseProviderError maps an error message to a { label, action } pair
    // using the errorCategories table. Returns null when msg is empty.
    categoriseProviderError: function (msg) {
      if (!msg) return null;
      for (const cat of this.errorCategories) {
        if (cat.pattern.test(msg)) {
          return { label: cat.label, action: cat.action };
        }
      }
      return { label: "error", action: "" };
    },
    // testProvider fires POST /api/providers/{id}/test and stores the result
    // in providerTestResult for display in the health card.
    testProvider: async function () {
      if (this.providerTestRunning) return;
      this.providerTestRunning = true;
      this.providerTestResult  = null;
      const id = this.selectedProviderID();
      try {
        const r = await fetch(`/api/providers/${encodeURIComponent(id)}/test`, {
          method:  "POST",
          headers: { "X-Dreamer-CSRF": this.csrf() },
        });
        if (!r.ok) {
          this.providerTestResult = { ok: false, error: `server error (HTTP ${r.status})`, category: "error" };
          return;
        }
        this.providerTestResult = await r.json();
      } catch (e) {
        this.providerTestResult = { ok: false, error: e.message, category: "error" };
      } finally {
        this.providerTestRunning = false;
      }
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
        const turns = this.values.providers[selected]?.max_turns;
        return this.positiveNumberError(turns, "Max turns");
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
        await this.loadProviderMeta();
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
        max_turns: this.numberOrNull(provider.max_turns),
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
