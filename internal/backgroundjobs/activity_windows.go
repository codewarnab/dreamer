//go:build windows

package backgroundjobs

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"dreamer/internal/logging"
)

// Windows constants for Job Object IO completion port messages.
const (
	jobObjectMsgNewProcess        = 7
	jobObjectMsgExitProcess       = 8
	jobObjectMsgActiveProcessZero = 4
)

// jobObjectAssociateCompletionPortInformation matches
// JOBOBJECT_ASSOCIATE_COMPLETION_PORT.
type jobObjectAssociateCompletionPortInformation struct {
	CompletionKey  uintptr
	CompletionPort windows.Handle
}

// Windows constants for GetExtendedTcpTable.
const (
	tcpTableOwnerPIDAll = 5  // TCP_TABLE_OWNER_PID_ALL
	afInet              = 2  // AF_INET
	afInet6             = 23 // AF_INET6
)

// mibTcpRowOwnerPID matches MIB_TCPROW_OWNER_PID.
type mibTcpRowOwnerPID struct {
	State      uint32
	LocalAddr  uint32
	LocalPort  uint32
	RemoteAddr uint32
	RemotePort uint32
	OwnerPID   uint32
}

var (
	modiphlpapi             = syscall.NewLazyDLL("iphlpapi.dll")
	procGetExtendedTcpTable = modiphlpapi.NewProc("GetExtendedTcpTable")
)

// ActivityMonitor tracks child processes and network connections during a
// background job run using the Job Object IO completion port (event-based,
// zero polling) and an end-of-run TCP table snapshot.
type ActivityMonitor struct {
	store       *ActivityStore
	logger      *logging.Logger
	jobHandle   uintptr
	providerPID uint32

	// PIDs seen during the run (for filtering TCP snapshot).
	mu      sync.Mutex
	seenPID map[uint32]string // PID -> executable name
}

// NewActivityMonitor creates a monitor for the given Job Object handle.
// The store is written to during monitoring; the caller must close it
// after Stop returns.
func NewActivityMonitor(jobHandle uintptr, providerPID uint32, store *ActivityStore, logger *logging.Logger) *ActivityMonitor {
	return &ActivityMonitor{
		store:       store,
		logger:      logger,
		jobHandle:   jobHandle,
		providerPID: providerPID,
		seenPID:     make(map[uint32]string),
	}
}

// Start begins monitoring the Job Object for process events. It attaches
// an IO completion port to the job and reads events in a background
// goroutine. Returns immediately; events are processed asynchronously.
// Call Stop when the job finishes to take a TCP snapshot and finalize.
func (m *ActivityMonitor) Start(ctx context.Context) error {
	// Create an IO completion port.
	port, err := windows.CreateIoCompletionPort(windows.InvalidHandle, 0, 0, 1)
	if err != nil {
		return fmt.Errorf("activity: create completion port: %w", err)
	}

	// Associate the completion port with the Job Object.
	info := jobObjectAssociateCompletionPortInformation{
		CompletionKey:  m.jobHandle,
		CompletionPort: port,
	}
	procSetInfo := syscall.NewLazyDLL("kernel32.dll").NewProc("SetInformationJobObject")
	ret, _, lastErr := procSetInfo.Call(
		m.jobHandle,
		uintptr(7), // JobObjectAssociateCompletionPortInformation
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)
	if ret == 0 {
		windows.CloseHandle(port)
		return fmt.Errorf("activity: associate completion port: %w", lastErr)
	}

	// Start the event reader goroutine.
	go m.readEvents(ctx, port)
	return nil
}

// readEvents blocks on the IO completion port and records process
// start/exit events. Runs until ctx is cancelled, then closes the port
// to unblock the IOCP goroutine immediately.
func (m *ActivityMonitor) readEvents(ctx context.Context, port windows.Handle) {
	procGetQ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetQueuedCompletionStatus")

	done := make(chan struct{})
	// Single goroutine blocking on the completion port. Closing the port
	// handle unblocks GetQueuedCompletionStatus immediately, giving us
	// clean shutdown without goroutine leaks or timeout delays.
	go func() {
		defer close(done)
		for {
			var numberOfBytes uint32
			var completionKey uintptr
			var overlapped *windows.Overlapped

			ret, _, _ := procGetQ.Call(
				uintptr(port),
				uintptr(unsafe.Pointer(&numberOfBytes)),
				uintptr(unsafe.Pointer(&completionKey)),
				uintptr(unsafe.Pointer(&overlapped)),
				0, // no timeout — blocks until event or port close
			)
			if ret == 0 {
				// Port closed or error — exit the goroutine.
				return
			}

			pid := uint32(numberOfBytes)
			msgID := completionKey

			switch msgID {
			case jobObjectMsgNewProcess:
				exeName := resolveProcessName(pid)
				m.mu.Lock()
				m.seenPID[pid] = exeName
				m.mu.Unlock()

				evt := ActivityEvent{
					Timestamp:   time.Now().UTC(),
					Type:        ActivityTypeProcessStart,
					PID:         pid,
					ProcessName: exeName,
					Category:    string(ClassifyProcess(exeName)),
				}
				if IsShellInterpreter(exeName) {
					evt.Detail = "SHELL_INTERPRETER"
				}
				if writeErr := m.store.Write(evt); writeErr != nil {
					m.logger.Warn("activity write failed", logging.Any("err", writeErr))
				}

			case jobObjectMsgExitProcess:
				m.mu.Lock()
				exeName := m.seenPID[pid]
				m.mu.Unlock()

				evt := ActivityEvent{
					Timestamp:   time.Now().UTC(),
					Type:        ActivityTypeProcessExit,
					PID:         pid,
					ProcessName: exeName,
				}
				if writeErr := m.store.Write(evt); writeErr != nil {
					m.logger.Warn("activity write failed", logging.Any("err", writeErr))
				}

			case jobObjectMsgActiveProcessZero:
				// All processes in the job have exited.
				return
			}
		}
	}()

	// Block until context is cancelled, then close the port to unblock
	// the goroutine's GetQueuedCompletionStatus call immediately.
	<-ctx.Done()
	windows.CloseHandle(port)
	<-done
}

// Stop takes a TCP table snapshot and returns the activity summary.
// Must be called after the provider process has exited.
func (m *ActivityMonitor) Stop() ActivitySummary {
	m.mu.Lock()
	procCount := len(m.seenPID)
	shellCount := 0
	for _, exeName := range m.seenPID {
		if IsShellInterpreter(exeName) {
			shellCount++
		}
	}
	m.mu.Unlock()

	connCount := m.snapshotTCPConnections()

	return ActivitySummary{
		Processes:         procCount,
		Connections:       connCount,
		ShellInterpreters: shellCount,
	}
}

// snapshotTCPConnections captures all TCP connections and filters to
// those owned by PIDs in the job's process tree. Returns the count.
func (m *ActivityMonitor) snapshotTCPConnections() int {
	connections := getTCPConnections(m.seenPID)
	count := 0
	for _, conn := range connections {
		if writeErr := m.store.Write(conn); writeErr != nil {
			m.logger.Warn("activity write failed", logging.Any("err", writeErr))
		}
		count++
	}
	return count
}

// getTCPConnections calls GetExtendedTcpTable and returns activity events
// for connections owned by any of the given PIDs.
const maxReverseDNSLookups = 5

func getTCPConnections(seenPID map[uint32]string) []ActivityEvent {
	var events []ActivityEvent
	dnsCache := make(map[string]string) // IP string → hostname
	dnsLookups := 0

	for _, family := range []uint32{afInet, afInet6} {
		rows := queryTCPTable(family)
		for _, row := range rows {
			if _, ok := seenPID[row.OwnerPID]; !ok {
				continue // not in our process tree
			}
			remoteIP := uint32ToIP(row.RemoteAddr)
			remotePort := uint16(row.RemotePort) // host byte order on Windows

			// Skip loopback and zero addresses.
			if remoteIP.IsLoopback() || remoteIP.IsUnspecified() {
				continue
			}

			ipStr := remoteIP.String()
			remoteAddr := fmt.Sprintf("%s:%d", ipStr, remotePort)

			// Reverse DNS: cached per IP, capped at maxReverseDNSLookups
			// to avoid blocking Stop() for many seconds on many connections.
			host := dnsCache[ipStr]
			if host == "" && dnsLookups < maxReverseDNSLookups {
				host = reverseDNS(remoteIP)
				dnsCache[ipStr] = host
				dnsLookups++
			}

			events = append(events, ActivityEvent{
				Timestamp:   time.Now().UTC(),
				Type:        ActivityTypeConnection,
				PID:         row.OwnerPID,
				ProcessName: seenPID[row.OwnerPID],
				RemoteAddr:  remoteAddr,
				RemoteHost:  host,
				State:       tcpStateToString(row.State),
			})
		}
	}
	return events
}

// queryTCPTable calls GetExtendedTcpTable for the given address family.
func queryTCPTable(family uint32) []mibTcpRowOwnerPID {
	var size uint32
	// First call to get the required buffer size.
	procGetExtendedTcpTable.Call(0, 0, 0, uintptr(family), uintptr(tcpTableOwnerPIDAll), 0)

	buf := make([]byte, 65536) // 64KB should be enough for most systems
	size = uint32(len(buf))

	ret, _, _ := procGetExtendedTcpTable.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
		0,
		uintptr(family),
		uintptr(tcpTableOwnerPIDAll),
		0,
	)
	if ret != 0 {
		return nil
	}

	// Parse the table: first 4 bytes are the row count.
	if size < 4 {
		return nil
	}
	count := *(*uint32)(unsafe.Pointer(&buf[0]))
	if count == 0 {
		return nil
	}

	rowSize := unsafe.Sizeof(mibTcpRowOwnerPID{})
	expected := 4 + int(count)*int(rowSize)
	if int(size) < expected {
		return nil
	}

	rows := make([]mibTcpRowOwnerPID, count)
	for i := uint32(0); i < count; i++ {
		offset := 4 + uintptr(i)*rowSize
		rows[i] = *(*mibTcpRowOwnerPID)(unsafe.Pointer(&buf[offset]))
	}
	return rows
}

// resolveProcessName gets the executable name for a PID.
func resolveProcessName(pid uint32) string {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return fmt.Sprintf("pid:%d", pid)
	}
	defer windows.CloseHandle(handle)

	var buf [windows.MAX_PATH]uint16
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err != nil {
		return fmt.Sprintf("pid:%d", pid)
	}
	fullPath := windows.UTF16ToString(buf[:size])

	// Return just the base name.
	for i := len(fullPath) - 1; i >= 0; i-- {
		if fullPath[i] == '\\' || fullPath[i] == '/' {
			return fullPath[i+1:]
		}
	}
	return fullPath
}

// reverseDNS performs a best-effort reverse DNS lookup with a short timeout.
func reverseDNS(ip net.IP) string {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	names, err := net.DefaultResolver.LookupAddr(ctx, ip.String())
	if err != nil || len(names) == 0 {
		return ""
	}
	return strings.TrimSuffix(names[0], ".")
}

// uint32ToIP converts a network-byte-order uint32 to net.IP.
func uint32ToIP(addr uint32) net.IP {
	return net.IPv4(byte(addr), byte(addr>>8), byte(addr>>16), byte(addr>>24))
}

// tcpStateToString converts a TCP state constant to a human-readable string.
func tcpStateToString(state uint32) string {
	switch state {
	case 1:
		return "CLOSED"
	case 2:
		return "LISTEN"
	case 3:
		return "SYN_SENT"
	case 4:
		return "SYN_RECEIVED"
	case 5:
		return "ESTABLISHED"
	case 6:
		return "FIN_WAIT_1"
	case 7:
		return "FIN_WAIT_2"
	case 8:
		return "CLOSE_WAIT"
	case 9:
		return "CLOSING"
	case 10:
		return "LAST_ACK"
	case 11:
		return "TIME_WAIT"
	case 12:
		return "DELETE_TCB"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", state)
	}
}
