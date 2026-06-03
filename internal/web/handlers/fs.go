package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"dreamer/internal/fsutil"
	"dreamer/internal/logging"
)

// FSExists returns an http.HandlerFunc for GET /api/fs/exists.
// Reports whether a given absolute path exists and whether it is a
// directory. Paths are restricted to configured project roots and the
// daemon output root to prevent arbitrary filesystem enumeration.
func FSExists(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		path := r.URL.Query().Get("path")
		if !filepath.IsAbs(path) {
			writeJSONError(w, http.StatusBadRequest, "path must be absolute")
			return
		}
		abs := filepath.Clean(path)
		// Resolve symlinks before containment check to prevent traversal
		// via symlink pointing outside project roots.
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolved
		}
		if !fsPathAllowed(deps, abs) {
			writeJSONError(w, http.StatusForbidden, "path outside configured project roots")
			return
		}
		fi, err := os.Stat(abs)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				writeJSON(w, http.StatusOK, map[string]any{
					"exists":   false,
					"is_dir":   false,
					"absolute": abs,
				})
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"exists":   true,
			"is_dir":   fi.IsDir(),
			"absolute": abs,
		})
	}
}

// fsPathAllowed returns true if abs is under any configured project root
// or the daemon output root.
func fsPathAllowed(deps Deps, abs string) bool {
	cfg := deps.Config()
	if cfg == nil {
		return false
	}
	for _, p := range cfg.Projects {
		if fsutil.PathWithinRoot(abs, p.Path) {
			return true
		}
	}
	if cfg.Daemon.OutputRoot != "" {
		if fsutil.PathWithinRoot(abs, cfg.Daemon.OutputRoot) {
			return true
		}
	}
	return false
}

// FSPickDirectory returns an http.HandlerFunc for POST /api/fs/pick-directory.
// Opens a native folder selection dialog on the server host machine.
func FSPickDirectory(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		ctx := r.Context()
		path, err := pickDirectoryFunc(ctx)
		if err != nil {
			deps.Logger.Error("fs pick directory failed", logging.ErrAttr(err)...)
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"path": path,
		})
	}
}

var pickDirectoryFunc = pickDirectory

// pickDirectory opens a native directory picker depending on the operating system.
func pickDirectory(ctx context.Context) (string, error) {
	switch runtime.GOOS {
	case "windows":
		// Windows: use PowerShell to open System.Windows.Forms FolderBrowserDialog.
		// We use a topmost parent window to bring the dialog to the front.
		timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()

		cmd := exec.CommandContext(timeoutCtx, "powershell", "-NoProfile", "-Command",
			`Add-Type -AssemblyName System.Windows.Forms;
$c1 = @'
using System;
using System.Runtime.InteropServices;
using System.Windows.Forms;

public class FolderPicker {
    [ComImport]
    [Guid("DC1C5A9C-E88A-4dde-A5A1-60F82A20AEF7")]
    class FileOpenDialog { }

    [ComImport]
    [Guid("42f85136-db7e-439c-85f1-e4075d135fc8")]
    [InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    interface IFileOpenDialog {
        [PreserveSig] int Show(IntPtr parent);
        void SetFileTypes();
        void SetFileTypeIndex();
        void GetFileTypeIndex();
        void Advise();
        void Unadvise();
        void SetOptions(int options);
        void GetOptions(out int options);
        void SetDefaultFolder();
        void SetFolder();
        void GetFolder();
        void GetCurrentSelection();
        void SetFileName();
        void GetFileName();
        void SetTitle([MarshalAs(UnmanagedType.LPWStr)] string title);
        void SetOkButtonLabel();
        void SetFileNameLabel();
        void GetResult(out IShellItem result);
    }

    [ComImport]
    [Guid("43826d1e-e718-42ee-bc55-a1e261c37bfe")]
    [InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    interface IShellItem {
        void BindToHandler();
        void GetParent();
        void GetDisplayName(uint sigdnName, out IntPtr ppszName);
    }

    public static string Show(IntPtr parent, string title) {
        var dialog = (IFileOpenDialog)new FileOpenDialog();
        dialog.SetOptions(0x00000020 | 0x00000008);
        if (!string.IsNullOrEmpty(title)) {
            dialog.SetTitle(title);
        }
        int hr = dialog.Show(parent);
        if (hr == 0) {
            IShellItem item;
            dialog.GetResult(out item);
            IntPtr pathPtr;
            item.GetDisplayName(0x80028000, out pathPtr);
            string path = Marshal.PtrToStringUni(pathPtr);
            Marshal.FreeCoTaskMem(pathPtr);
            return path;
        }
        return null;
    }
}
'@;
Add-Type -TypeDefinition $c1 -ReferencedAssemblies 'System.Windows.Forms';
$c2 = '[DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();';
$type = Add-Type -MemberDefinition $c2 -Name Win32Utils -PassThru;
$hwnd = $type::GetForegroundWindow();
$path = [FolderPicker]::Show($hwnd, "Select Project Folder");
Write-Output $path`)
		out, err := cmd.Output()
		if err != nil {
			return "", err
		}
		path := strings.TrimSpace(string(out))
		if path == "" {
			return "", errors.New("no folder selected or user canceled")
		}
		return path, nil

	case "darwin":
		// macOS: use osascript to choose folder
		timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()

		cmd := exec.CommandContext(timeoutCtx, "osascript", "-e", `POSIX path of (choose folder with prompt "Select Project Folder")`)
		out, err := cmd.Output()
		if err != nil {
			return "", err
		}
		path := strings.TrimSpace(string(out))
		if path == "" {
			return "", errors.New("no folder selected or user canceled")
		}
		return path, nil

	case "linux":
		// Linux: try zenity or kdialog
		timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()

		if _, err := exec.LookPath("zenity"); err == nil {
			cmd := exec.CommandContext(timeoutCtx, "zenity", "--file-selection", "--directory", "--title=Select Project Folder")
			out, err := cmd.Output()
			if err != nil {
				return "", err
			}
			path := strings.TrimSpace(string(out))
			if path == "" {
				return "", errors.New("no folder selected or user canceled")
			}
			return path, nil
		}
		if _, err := exec.LookPath("kdialog"); err == nil {
			cmd := exec.CommandContext(timeoutCtx, "kdialog", "--getexistingdirectory")
			out, err := cmd.Output()
			if err != nil {
				return "", err
			}
			path := strings.TrimSpace(string(out))
			if path == "" {
				return "", errors.New("no folder selected or user canceled")
			}
			return path, nil
		}
		return "", errors.New("no folder selection tool available (install zenity or kdialog)")

	default:
		return "", fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
}
