//go:build windows

package cmd

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	"dreamer/internal/procutil"
)

// detachedProcessAttr returns SysProcAttr that detaches the child from the
// parent's console. DETACHED_PROCESS prevents the child from inheriting the
// console; CREATE_NEW_PROCESS_GROUP gives it its own group (enables Ctrl+C
// independence).
func detachedProcessAttr() *syscall.SysProcAttr {
	const detachedProcess = 0x00000008 // DETACHED_PROCESS — child gets no inherited console
	return &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess,
	}
}

// suppressConsoleWindow detaches the current process from its console,
// closing the window. Used by background job commands that log to files.
func suppressConsoleWindow() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	if proc := kernel32.NewProc("FreeConsole"); proc.Find() == nil {
		// FreeConsole returns nonzero on success; ignore error — best-effort
		// cleanup and the caller has no actionable recovery.
		proc.Call() //nolint:errcheck
	}
}

// killDaemon uses taskkill to terminate the daemon and its entire process tree.
// /T kills child processes (exec.CommandContext on Windows only kills the direct
// child). /F forces termination since console apps don't receive WM_CLOSE.
// Returns nil if the process is already gone (taskkill exit code 128).
func killDaemon(pid int) error {
	cmd := exec.Command("taskkill", "/PID", fmt.Sprintf("%d", pid), "/T", "/F")
	procutil.SetNoWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := string(out)
		// taskkill exit code 128 = "process not found". Treat as success since
		// the process is already gone — a race between IsProcessAlive and here.
		if strings.Contains(msg, "not found") {
			return nil
		}
		if strings.Contains(msg, "Access is denied") {
			return fmt.Errorf("access denied killing PID %d: run as Administrator or use the same user session that started the daemon: %w", pid, err)
		}
		if len(out) > 0 {
			return fmt.Errorf("%s: %w", strings.TrimSpace(msg), err)
		}
		return err
	}
	return nil
}

var (
	modiphlpapi             = syscall.NewLazyDLL("iphlpapi.dll")
	procGetExtendedTcpTable = modiphlpapi.NewProc("GetExtendedTcpTable")
)

const (
	tcpTableOwnerPIDAll = 5  // TCP_TABLE_OWNER_PID_ALL
	afInet              = 2  // AF_INET (IPv4)
	afInet6             = 23 // AF_INET6 (IPv6)
	tcpStateListen      = 2  // MIB_TCP_STATE_LISTEN
)

// mibTcpRowOwnerPID matches Windows MIB_TCPROW_OWNER_PID (IPv4).
type mibTcpRowOwnerPID struct {
	State      uint32
	LocalAddr  uint32
	LocalPort  uint32
	RemoteAddr uint32
	RemotePort uint32
	OwnerPID   uint32
}

// mibTcp6RowOwnerPID matches Windows MIB_TCP6ROW_OWNER_PID (IPv6).
type mibTcp6RowOwnerPID struct {
	LocalAddr     [16]byte
	LocalScopeID  uint32
	LocalPort     uint32
	RemoteAddr    [16]byte
	RemoteScopeID uint32
	RemotePort    uint32
	State         uint32
	OwnerPID      uint32
}

// decodePort converts a 32-bit port field (with lower 16 bits in network byte
// order / big-endian) to a host integer port.
func decodePort(portField uint32) int {
	return int((portField&0xff)<<8 | (portField>>8)&0xff)
}

// getExtendedTCPTable calls GetExtendedTcpTable from iphlpapi.dll for the given
// address family (afInet or afInet6) and returns the populated buffer.
func getExtendedTCPTable(family uint32) ([]byte, error) {
	// Start with 64 KiB, empirically sufficient for thousands of sockets.
	var size uint32 = 65536
	buf := make([]byte, size)

	const errorInsufficientBuffer = 122

	for attempts := 0; attempts < 3; attempts++ {
		ret, _, _ := procGetExtendedTcpTable.Call(
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(unsafe.Pointer(&size)),
			0, // bOrder = false (sorting not needed)
			uintptr(family),
			uintptr(tcpTableOwnerPIDAll),
			0, // reserved
		)
		if ret == 0 {
			return buf[:size], nil
		}
		if ret != errorInsufficientBuffer {
			return nil, fmt.Errorf("GetExtendedTcpTable: win32 error %d", ret)
		}
		// Buffer was insufficient; size now holds the required size.
		buf = make([]byte, size)
	}
	return nil, fmt.Errorf("GetExtendedTcpTable: buffer allocation failed after retries")
}

func findPIDInIPv4Table(buf []byte, targetPort int) int {
	if len(buf) < 4 {
		return 0
	}
	numEntries := int(*(*uint32)(unsafe.Pointer(&buf[0])))
	rowSize := int(unsafe.Sizeof(mibTcpRowOwnerPID{}))
	if len(buf) < 4+numEntries*rowSize {
		return 0
	}

	for i := 0; i < numEntries; i++ {
		offset := 4 + uintptr(i)*uintptr(rowSize)
		row := (*mibTcpRowOwnerPID)(unsafe.Pointer(&buf[offset]))
		if row.State == tcpStateListen && decodePort(row.LocalPort) == targetPort && row.OwnerPID > 0 {
			return int(row.OwnerPID)
		}
	}
	return 0
}

func findPIDInIPv6Table(buf []byte, targetPort int) int {
	if len(buf) < 4 {
		return 0
	}
	numEntries := int(*(*uint32)(unsafe.Pointer(&buf[0])))
	rowSize := int(unsafe.Sizeof(mibTcp6RowOwnerPID{}))
	if len(buf) < 4+numEntries*rowSize {
		return 0
	}

	for i := 0; i < numEntries; i++ {
		offset := 4 + uintptr(i)*uintptr(rowSize)
		row := (*mibTcp6RowOwnerPID)(unsafe.Pointer(&buf[offset]))
		if row.State == tcpStateListen && decodePort(row.LocalPort) == targetPort && row.OwnerPID > 0 {
			return int(row.OwnerPID)
		}
	}
	return 0
}

// FindPIDByPort finds the PID of the process listening on the given TCP port.
// It queries the Win32 TCP connection table in-memory via GetExtendedTcpTable
// (iphlpapi.dll), eliminating child process overhead (cmd, netstat, findstr)
// and preventing port substring collisions (e.g., port 80 matching 180 or 8080).
func FindPIDByPort(port int) (int, error) {
	if port <= 0 || port > 65535 {
		return 0, fmt.Errorf("invalid port: %d", port)
	}

	// 1. Check IPv4 listening sockets
	ipv4Buf, err4 := getExtendedTCPTable(afInet)
	if err4 == nil {
		if pid := findPIDInIPv4Table(ipv4Buf, port); pid > 0 {
			return pid, nil
		}
	}

	// 2. Check IPv6 listening sockets (e.g. dual-stack listeners like [::]:port)
	ipv6Buf, err6 := getExtendedTCPTable(afInet6)
	if err6 == nil {
		if pid := findPIDInIPv6Table(ipv6Buf, port); pid > 0 {
			return pid, nil
		}
	}

	// If both table queries returned system errors, report the failure.
	if err4 != nil && err6 != nil {
		return 0, fmt.Errorf("query tcp table: ipv4: %v; ipv6: %w", err4, err6)
	}

	return 0, fmt.Errorf("no process found listening on port %d", port)
}
