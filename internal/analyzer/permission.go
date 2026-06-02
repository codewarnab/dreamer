package analyzer

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"dreamer/internal/fsutil"
)

// shellWriteIdiomRE matches common shell write idioms that the upstream
// classifier does not always flag: writers (tee, dd, …), in-place editors
// (sed -i, perl -pi), shell wrappers (bash -c, sh -c) that can hide writes,
// process-substitution write side `>(...)`, and inline interpreter -e/-pi.
//
// All command-name alternates anchor at command position — start of line, or
// after a shell separator (`;`, `&`, `|`, `(`, `$(`) — so legitimate reads
// like `cat patch.txt` or `grep 'tee' README.md` are not denied.
var shellCmdAnchor = `(?:^|[;&|(]|\$\()\s*`

var shellWriteIdiomRE = regexp.MustCompile(
	`(?i)` +
		shellCmdAnchor + `(tee|dd|rsync|mkfifo|mknod|truncate)\b` +
		`|` + shellCmdAnchor + `(bash|sh|zsh|ksh|dash|ash|fish|csh|tcsh)\s+-c\b` +
		`|` + shellCmdAnchor + `sed\s+(-i\b|--in-place\b)` +
		`|` + shellCmdAnchor + `(perl|python\d*|ruby|node|tcl)\s+(-i\b|-pi\b|-e\b)` +
		`|>\(`,
)

// DecidePermission applies the read-only sandbox rules from §4.5 to a
// provider-agnostic permission request. Resolved working directory must be
// absolute (caller's responsibility); empty means "no path scoping".
func DecidePermission(req PermissionRequest, normalizedRoot string) PermissionDecision {
	switch req.Kind {
	case PermissionKindRead:
		return decideFilesystem(req, normalizedRoot)
	case PermissionKindURL:
		return validateURL(req)
	case PermissionKindShell:
		if !shellRequestReadOnly(req) {
			return PermissionDecision{Reason: "shell request is not read-only"}
		}
		return decideFilesystem(req, normalizedRoot)
	case PermissionKindMCPTool, PermissionKindCustomTool:
		if req.ReadOnly != nil && *req.ReadOnly {
			return PermissionDecision{Approved: true}
		}
		return PermissionDecision{Reason: "tool request is not read-only"}
	default:
		return PermissionDecision{Reason: fmt.Sprintf("permission kind %q is not allowed in read-only analysis mode", req.Kind)}
	}
}

// validateURL checks URL permission requests against SSRF risks.
// Only http/https schemes are allowed. Loopback, link-local, and private
// IP ranges are denied to prevent access to cloud metadata services and
// internal network endpoints.
//
// LIMITATION: This check is point-in-time only. The actual HTTP request
// is made by the external SDK/agent subprocess. 3xx redirects are
// followed by the agent's HTTP client without re-checking with dreamer,
// so an attacker controlling an approved host can redirect to a
// restricted IP. Closing this requires a dreamer-controlled forward
// proxy that re-validates each hop's resolved IP at connect time.
func validateURL(req PermissionRequest) PermissionDecision {
	var rawURL string
	if req.Path != nil && *req.Path != "" {
		rawURL = *req.Path
	} else if len(req.PossiblePaths) > 0 {
		rawURL = req.PossiblePaths[0]
	}
	if rawURL == "" {
		// Provider didn't supply a URL; cannot block what we can't see.
		return PermissionDecision{Approved: true}
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return PermissionDecision{Reason: fmt.Sprintf("URL %q is not parseable: %v", rawURL, err)}
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return PermissionDecision{Reason: fmt.Sprintf("URL scheme %q is not allowed; only http/https permitted", parsed.Scheme)}
	}
	hostname := parsed.Hostname()
	if hostname == "" {
		return PermissionDecision{Reason: "URL has no hostname"}
	}
	// Check if hostname is an IP literal.
	if ip := net.ParseIP(hostname); ip != nil {
		if isRestrictedIP(ip) {
			return PermissionDecision{Reason: fmt.Sprintf("URL targets restricted IP %s", ip)}
		}
		return PermissionDecision{Approved: true}
	}
	// Hostname is a domain name. Block common metadata/local names.
	lower := strings.ToLower(hostname)
	if lower == "localhost" || strings.HasSuffix(lower, ".local") {
		return PermissionDecision{Reason: fmt.Sprintf("URL targets local hostname %q", hostname)}
	}
	// Resolve DNS once and check all returned IPs. This provides a
	// point-in-time check that the hostname doesn't resolve to a
	// restricted IP at permission time. Note: this does NOT prevent
	// DNS rebinding — the actual HTTP request is made by the external
	// SDK/agent subprocess, which re-resolves the hostname independently.
	// A malicious DNS server with a short TTL could return a safe IP at
	// permission time and a restricted IP at dial time. ApprovedIP is
	// stored for potential future use by a dreamer-controlled proxy.
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return PermissionDecision{Reason: fmt.Sprintf("DNS lookup for %q failed: %v", hostname, err)}
	}
	if len(ips) == 0 {
		return PermissionDecision{Reason: fmt.Sprintf("DNS lookup for %q returned no addresses", hostname)}
	}
	// Deny if ANY resolved IP is restricted (defense-in-depth).
	for _, ip := range ips {
		if isRestrictedIP(ip) {
			return PermissionDecision{Reason: fmt.Sprintf("hostname %q resolves to restricted IP %s", hostname, ip)}
		}
	}
	// Return the first resolved IP for pinning. Currently stored on
	// the decision but not consumed by callers (acpcore, copilotsdk)
	// because the HTTP client is controlled by the SDK/agent, not by
	// dreamer. A future SDK version or custom dialer could use this
	// to make DNS rebinding ineffective.
	return PermissionDecision{Approved: true, ApprovedIP: ips[0].String()}
}

// cgnatRange covers Carrier-Grade NAT (RFC 6598) which is not included in
// Go's IsPrivate() but should not be reachable from the analyzer.
var cgnatRange = net.IPNet{IP: net.IPv4(100, 64, 0, 0).To4(), Mask: net.CIDRMask(10, 32)}

// nat64Prefix is the Well-Known NAT64 prefix (RFC 6052).
var nat64Prefix = net.IPNet{IP: net.ParseIP("64:ff9b::"), Mask: net.CIDRMask(96, 128)}

// sixToFourPrefix is the 6to4 relay prefix (RFC 3056).
var sixToFourPrefix = net.IPNet{IP: net.ParseIP("2002::"), Mask: net.CIDRMask(16, 128)}

// isRestrictedIP returns true for IPs that must not be dialed by the analyzer:
// loopback, link-local, private, unspecified (0.0.0.0 / ::), multicast,
// and CGNAT (100.64.0.0/10). IPv4-mapped IPv6 (e.g. ::ffff:127.0.0.1),
// NAT64 (64:ff9b::/96), and 6to4 (2002::/16) embedded IPv4 are unwrapped
// and checked recursively.
func isRestrictedIP(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	// Unwrap NAT64 embedded IPv4 (last 4 bytes of 64:ff9b::<v4>).
	if nat64Prefix.Contains(ip) && len(ip) == net.IPv6len {
		return isRestrictedIP(net.IP(ip[12:16]))
	}
	// Unwrap 6to4 embedded IPv4 (bytes 2-5 of 2002:<v4>::...).
	if sixToFourPrefix.Contains(ip) && len(ip) == net.IPv6len {
		return isRestrictedIP(net.IP(ip[2:6]))
	}
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() || cgnatRange.Contains(ip)
}

func decideFilesystem(req PermissionRequest, normalizedRoot string) PermissionDecision {
	// Bx: fail closed when no project root has been configured. Callers
	// always set WorkingDirectory today; a future caller forgetting to set
	// it must not get an unrestricted analyzer with read access anywhere
	// on disk.
	if normalizedRoot == "" {
		return PermissionDecision{Reason: "no project root configured; refusing filesystem access in read-only analysis mode"}
	}
	candidates := requestPathCandidates(req)
	if len(candidates) == 0 {
		return PermissionDecision{Reason: "filesystem request did not include candidate paths to validate"}
	}
	for _, candidate := range candidates {
		normalized, err := normalizeCandidatePath(candidate, normalizedRoot)
		if err != nil {
			return PermissionDecision{Reason: fmt.Sprintf("filesystem path %q is invalid or ambiguous after symlink resolution: %v", candidate, err)}
		}
		if !fsutil.PathWithinRoot(normalized, normalizedRoot) {
			return PermissionDecision{Reason: fmt.Sprintf("filesystem path %q resolves outside project root %q after symlink resolution (resolved to %q)", candidate, normalizedRoot, normalized)}
		}
	}
	return PermissionDecision{Approved: true}
}

func requestPathCandidates(req PermissionRequest) []string {
	candidates := make([]string, 0, len(req.PossiblePaths)+1)
	if req.Path != nil {
		candidates = append(candidates, *req.Path)
	}
	candidates = append(candidates, req.PossiblePaths...)
	return candidates
}

func shellRequestReadOnly(req PermissionRequest) bool {
	if req.HasWriteFileRedirection != nil && *req.HasWriteFileRedirection {
		return false
	}
	// Second-pass deny: even when the SDK says read-only, reject known write
	// idioms in the raw command text (B2). The SDK redirection detector
	// handles `>` / `>>` only; it misses tee, dd, sed -i, bash -c wrappers,
	// process substitution, etc.
	if req.FullCommandText != nil && shellWriteIdiomRE.MatchString(*req.FullCommandText) {
		return false
	}
	if req.ReadOnly != nil {
		return *req.ReadOnly
	}
	if len(req.Commands) == 0 {
		return false
	}
	for _, cmd := range req.Commands {
		if !cmd.ReadOnly {
			return false
		}
	}
	return true
}

// normalizeCandidatePath returns the absolute, symlink-resolved candidate path.
// Non-existent leaves resolve via the deepest existing ancestor; resolver errors deny.
func normalizeCandidatePath(candidate string, normalizedRoot string) (string, error) {
	trimmed := strings.TrimSpace(candidate)
	if trimmed == "" {
		return "", fmt.Errorf("path is empty")
	}
	if strings.ContainsRune(trimmed, '\x00') {
		return "", fmt.Errorf("path contains null byte")
	}
	if looksLikeNonFilesystemPath(trimmed) {
		return "", fmt.Errorf("path appears to be non-filesystem")
	}
	candidate = trimmed
	if isVolumeRelativeRootedPath(candidate) {
		candidate = filepath.VolumeName(normalizedRoot) + candidate
	}
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(normalizedRoot, candidate)
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	return fsutil.ResolveSymlinks(abs)
}

func looksLikeNonFilesystemPath(candidate string) bool {
	lower := strings.ToLower(candidate)
	return strings.Contains(lower, "://") || strings.HasPrefix(lower, "file:")
}

func isVolumeRelativeRootedPath(candidate string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	return filepath.VolumeName(candidate) == "" && strings.HasPrefix(filepath.Clean(candidate), string(filepath.Separator))
}
