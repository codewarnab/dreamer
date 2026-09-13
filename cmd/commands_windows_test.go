//go:build windows

package cmd

import (
	"net"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"unsafe"
)

func TestDetachedProcessAttr(t *testing.T) {
	attr := detachedProcessAttr()
	if attr == nil {
		t.Fatal("detachedProcessAttr returned nil")
	}
	expected := syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008 // DETACHED_PROCESS
	if attr.CreationFlags != uint32(expected) {
		t.Fatalf("CreationFlags = 0x%x, want 0x%x", attr.CreationFlags, expected)
	}
}

func TestKillDaemon_ProcessNotRunning(t *testing.T) {
	// Start a short-lived process, wait for it to exit, then use its
	// guaranteed-defunct PID — avoids hardcoding a PID that might collide
	// with a real process on long-running hosts.
	cmd := exec.Command("cmd", "/c", "exit", "0")
	if err := cmd.Run(); err != nil {
		t.Fatalf("cmd.Run: %v", err)
	}
	err := killDaemon(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("expected nil for defunct PID, got: %v", err)
	}
}

func encodePortForTest(port int) uint32 {
	high := byte(port >> 8)
	low := byte(port & 0xff)
	return uint32(high) | (uint32(low) << 8)
}

func buildIPv4TableBuffer(rows []mibTcpRowOwnerPID) []byte {
	buf := make([]byte, 4+len(rows)*int(unsafe.Sizeof(mibTcpRowOwnerPID{})))
	*(*uint32)(unsafe.Pointer(&buf[0])) = uint32(len(rows))
	rowSize := unsafe.Sizeof(mibTcpRowOwnerPID{})
	for i, r := range rows {
		offset := 4 + uintptr(i)*rowSize
		*(*mibTcpRowOwnerPID)(unsafe.Pointer(&buf[offset])) = r
	}
	return buf
}

func buildIPv6TableBuffer(rows []mibTcp6RowOwnerPID) []byte {
	buf := make([]byte, 4+len(rows)*int(unsafe.Sizeof(mibTcp6RowOwnerPID{})))
	*(*uint32)(unsafe.Pointer(&buf[0])) = uint32(len(rows))
	rowSize := unsafe.Sizeof(mibTcp6RowOwnerPID{})
	for i, r := range rows {
		offset := 4 + uintptr(i)*rowSize
		*(*mibTcp6RowOwnerPID)(unsafe.Pointer(&buf[offset])) = r
	}
	return buf
}

func TestDecodePort(t *testing.T) {
	testPorts := []int{1, 80, 180, 443, 8080, 65535}
	for _, port := range testPorts {
		encoded := encodePortForTest(port)
		decoded := decodePort(encoded)
		if decoded != port {
			t.Fatalf("decodePort(%d) = %d, want %d", encoded, decoded, port)
		}
	}
}

func TestFindPIDInIPv4Table_NoSubstringCollision(t *testing.T) {
	table := []mibTcpRowOwnerPID{
		{State: tcpStateListen, LocalPort: encodePortForTest(8080), OwnerPID: 1001},
		{State: tcpStateListen, LocalPort: encodePortForTest(180), OwnerPID: 1002},
		{State: tcpStateListen, LocalPort: encodePortForTest(80), OwnerPID: 1003},
		{State: 5 /* ESTABLISHED */, LocalPort: encodePortForTest(80), OwnerPID: 1004},
	}
	buf := buildIPv4TableBuffer(table)

	if pid := findPIDInIPv4Table(buf, 80); pid != 1003 {
		t.Fatalf("findPIDInIPv4Table for port 80 = %d, want 1003", pid)
	}
	if pid := findPIDInIPv4Table(buf, 8080); pid != 1001 {
		t.Fatalf("findPIDInIPv4Table for port 8080 = %d, want 1001", pid)
	}
	if pid := findPIDInIPv4Table(buf, 180); pid != 1002 {
		t.Fatalf("findPIDInIPv4Table for port 180 = %d, want 1002", pid)
	}
	if pid := findPIDInIPv4Table(buf, 9999); pid != 0 {
		t.Fatalf("findPIDInIPv4Table for unlisted port = %d, want 0", pid)
	}
}

func TestFindPIDInIPv6Table_NoSubstringCollision(t *testing.T) {
	table := []mibTcp6RowOwnerPID{
		{State: tcpStateListen, LocalPort: encodePortForTest(8080), OwnerPID: 2001},
		{State: tcpStateListen, LocalPort: encodePortForTest(180), OwnerPID: 2002},
		{State: tcpStateListen, LocalPort: encodePortForTest(80), OwnerPID: 2003},
		{State: 5 /* ESTABLISHED */, LocalPort: encodePortForTest(80), OwnerPID: 2004},
	}
	buf := buildIPv6TableBuffer(table)

	if pid := findPIDInIPv6Table(buf, 80); pid != 2003 {
		t.Fatalf("findPIDInIPv6Table for port 80 = %d, want 2003", pid)
	}
	if pid := findPIDInIPv6Table(buf, 8080); pid != 2001 {
		t.Fatalf("findPIDInIPv6Table for port 8080 = %d, want 2001", pid)
	}
	if pid := findPIDInIPv6Table(buf, 180); pid != 2002 {
		t.Fatalf("findPIDInIPv6Table for port 180 = %d, want 2002", pid)
	}
	if pid := findPIDInIPv6Table(buf, 443); pid != 0 {
		t.Fatalf("findPIDInIPv6Table for unlisted port = %d, want 0", pid)
	}
}

func TestFindPIDByPort_Live(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	pid, err := FindPIDByPort(port)
	if err != nil {
		t.Fatalf("FindPIDByPort(%d): %v", port, err)
	}
	if pid != os.Getpid() {
		t.Fatalf("FindPIDByPort(%d) = %d, want current PID %d", port, pid, os.Getpid())
	}
}

func TestFindPIDByPort_InvalidPorts(t *testing.T) {
	for _, port := range []int{0, -1, 65536, 100000} {
		if _, err := FindPIDByPort(port); err == nil {
			t.Fatalf("expected error for invalid port %d, got nil", port)
		}
	}
}
