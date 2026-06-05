window.logsPage = function () {
  return {
    raw: "",
    level: "all",
    autoScroll: true,
    autoRefresh: false,
    searchQuery: "",
    lineLimit: 500,
    loading: false,
    copied: false,
    parsedLines: [],
    _timer: null,

    // Bootstraps state triggers and active listeners
    initPage: function () {
      this.load();
      
      // Perform an automatic full reload when severity changes
      this.$watch('level', () => this.load());
      
      // Listen to autoRefresh changes to configure polling
      this.$watch('autoRefresh', v => v ? this.start() : this.stop());
    },

    // Fetches severity-filtered log tails directly from Go server
    // Highly scalable: offloads parsing/filtering payload to the server
    load: async function () {
      if (this.loading) return;
      this.loading = true;
      try {
        // Append backend query level if active
        const url = this.level === 'all' ? "/api/logs/tail" : `/api/logs/tail?level=${encodeURIComponent(this.level)}`;
        const r = await fetch(url);
        if (!r.ok) throw new Error("HTTP error " + r.status);
        
        const content = await r.text();
        this.raw = content;
        
        // High-performance parser execution in background thread lifecycle
        this.parsedLines = this.parseLogs(content);
        
        // AutoScroll to bottom of active logs pre element if selected
        this.$nextTick(() => {
          if (this.autoScroll && this.$refs.container) {
            this.$refs.container.scrollTop = this.$refs.container.scrollHeight;
          }
        });
      } catch (e) {
        console.error("Failed to load logs:", e);
      } finally {
        this.loading = false;
      }
    },

    // High performance log line parsing (slog key-value standard format)
    parseLogs: function (rawText) {
      if (!rawText) return [];
      const lines = rawText.split("\n");
      const parsed = [];
      for (let i = 0; i < lines.length; i++) {
        const ln = lines[i];
        if (!ln.trim()) continue;
        parsed.push(this.parseLine(ln));
      }
      return parsed;
    },

    // Slog parser handles key=value tokens, taking care of nested quotes & escaped quotes.
    parseLine: function (line) {
      const hasLevel = line.includes("level=");
      if (!hasLevel) {
        // Fallback for raw traceback blocks or child process outputs
        return {
          raw: line,
          isStructured: false,
          level: "info",
          time: "",
          msg: line,
          attrs: []
        };
      }

      const fields = {};
      let currentKey = "";
      let currentValue = "";
      let inQuotes = false;
      let isKey = true;

      // Single-pass tokenizer loop
      for (let i = 0; i < line.length; i++) {
        const char = line[i];
        if (isKey) {
          if (char === "=") {
            isKey = false;
          } else if (char === " ") {
            continue;
          } else {
            currentKey += char;
          }
        } else {
          if (char === '"') {
            if (inQuotes && line[i - 1] === "\\") {
              currentValue += char;
            } else {
              inQuotes = !inQuotes;
            }
          } else if (char === " " && !inQuotes) {
            fields[currentKey] = currentValue;
            currentKey = "";
            currentValue = "";
            isKey = true;
          } else {
            currentValue += char;
          }
        }
      }
      
      // Handle last trailing token
      if (currentKey) {
        fields[currentKey] = currentValue;
      }

      // Extract well-known slog keys
      let timeVal = fields["time"] || "";
      let levelVal = (fields["level"] || "info").toLowerCase();
      let msgVal = fields["msg"] || "";

      delete fields["time"];
      delete fields["level"];
      delete fields["msg"];

      // Map dynamic secondary keys to attribute arrays
      const attrs = [];
      for (const k in fields) {
        attrs.push({ key: k, value: fields[k] });
      }

      return {
        raw: line,
        isStructured: true,
        time: timeVal,
        level: levelVal,
        msg: msgVal,
        attrs: attrs
      };
    },

    // Client-side instant query filtering & search matching
    filteredLines: function () {
      let lines = this.parsedLines;
      const q = this.searchQuery.trim().toLowerCase();
      
      if (q) {
        lines = lines.filter(l => {
          if (l.msg.toLowerCase().includes(q)) return true;
          if (l.time.toLowerCase().includes(q)) return true;
          if (l.level.toLowerCase().includes(q)) return true;
          for (let i = 0; i < l.attrs.length; i++) {
            if (l.attrs[i].key.toLowerCase().includes(q) || l.attrs[i].value.toLowerCase().includes(q)) return true;
          }
          return false;
        });
      }

      // Restrict maximum lines in viewport to scale DOM rendering
      if (lines.length > this.lineLimit) {
        lines = lines.slice(lines.length - this.lineLimit);
      }
      return lines;
    },

    // Secures text and wraps query terms in highlighted HTML tags
    highlightAndEscape: function (text) {
      const escaped = this.escapeHTML(text);
      const q = this.searchQuery.trim();
      if (!q) return escaped;
      
      const escapedQuery = this.escapeHTML(q).replace(/[-\/\\^$*+?.()|[\]{}]/g, '\\$&');
      if (!escapedQuery) return escaped;
      
      const regex = new RegExp(`(${escapedQuery})`, "gi");
      return escaped.replace(regex, '<mark class="log-search-highlight">$1</mark>');
    },

    // Safe HTML encoder to avoid XSS injections from log details
    escapeHTML: function (str) {
      return str
        .replace(/&/g, "&amp;")
        .replace(/</g, "&lt;")
        .replace(/>/g, "&gt;")
        .replace(/"/g, "&quot;")
        .replace(/'/g, "&#039;");
    },

    // Compact timestamp formatter showing HH:MM:SS format
    formatTime: function (timeStr) {
      if (timeStr.length >= 19) {
        return timeStr.substring(11, 19);
      }
      return timeStr;
    },

    // Safely copies current active logs to clipboard
    copyToClipboard: async function () {
      const text = this.filteredLines().map(l => l.raw).join("\n");
      try {
        await navigator.clipboard.writeText(text);
        this.copied = true;
        setTimeout(() => { this.copied = false; }, 2000);
      } catch (e) {
        console.error("Failed to copy logs:", e);
      }
    },

    // Auto-refresh timer routines
    start: function () {
      if (this._timer) return;
      this._timer = setInterval(() => {
        if (!this.autoRefresh) {
          this.stop();
          return;
        }
        this.load();
      }, 3000);
    },

    stop: function () {
      if (this._timer) {
        clearInterval(this._timer);
        this._timer = null;
      }
    },

    destroy: function () {
      this.stop();
    }
  };
};
