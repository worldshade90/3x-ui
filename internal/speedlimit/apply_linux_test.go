//go:build linux

package speedlimit

import (
	"context"
	"net"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestApplyCapsMarkedUploadAndDownload(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("nftables apply test needs root")
	}
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("nft not installed")
	}
	ctx := context.Background()
	ns := "xui-sl-t"
	hostIf := "xuislt0"
	peerIf := "xuislt1"
	cleanupNet := func() {
		_ = exec.Command("ip", "netns", "del", ns).Run()
		_ = exec.Command("ip", "link", "del", hostIf).Run()
	}
	cleanupNet()
	t.Cleanup(func() {
		_ = Apply(context.Background(), nil)
		cleanupNet()
	})
	if err := exec.Command("ip", "netns", "add", ns).Run(); err != nil {
		t.Fatalf("netns: %v", err)
	}
	if err := exec.Command("ip", "link", "add", hostIf, "type", "veth", "peer", "name", peerIf).Run(); err != nil {
		t.Fatalf("veth: %v", err)
	}
	must(t, exec.Command("ip", "link", "set", peerIf, "netns", ns).Run())
	must(t, exec.Command("ip", "addr", "add", "10.99.8.1/24", "dev", hostIf).Run())
	must(t, exec.Command("ip", "link", "set", hostIf, "up").Run())
	must(t, exec.Command("ip", "netns", "exec", ns, "ip", "addr", "add", "10.99.8.2/24", "dev", peerIf).Run())
	must(t, exec.Command("ip", "netns", "exec", ns, "ip", "link", "set", peerIf, "up").Run())
	must(t, exec.Command("ip", "netns", "exec", ns, "ip", "link", "set", "lo", "up").Run())

	const (
		port = "9107"
		mark = 0x3A000002
	)
	lnCmd := exec.Command("ip", "netns", "exec", ns, "python3", "-c", peerScript)
	lnCmd.Env = append(os.Environ(), "PORT="+port)
	if err := lnCmd.Start(); err != nil {
		t.Fatalf("peer: %v", err)
	}
	t.Cleanup(func() { _ = lnCmd.Process.Kill() })
	time.Sleep(200 * time.Millisecond)

	client := []Client{{ID: 2, Email: "live@x", DownMbit: 1, UpMbit: 1}}
	if err := Apply(ctx, client); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// nft's byte bucket starts with about one second of credit, so the payload
	// has to be longer than that or the whole transfer fits in the burst.
	const payload = 256 * 1024
	limitedUp := measureUpload(t, "10.99.8.2:"+port, mark, payload)
	if limitedUp < 500*time.Millisecond || limitedUp > 5*time.Second {
		t.Fatalf("marked upload of 256KiB at 1 Mbit/s took %s", limitedUp)
	}
	plainUp := measureUpload(t, "10.99.8.2:"+port, 0, payload)
	if plainUp > 200*time.Millisecond {
		t.Fatalf("unmarked upload took %s, cap leaked onto other sockets", plainUp)
	}

	limitedDown := measureDownload(t, "10.99.8.2:"+port, mark, payload)
	if limitedDown < 500*time.Millisecond || limitedDown > 5*time.Second {
		t.Fatalf("marked download of 256KiB at 1 Mbit/s took %s", limitedDown)
	}

	if err := Apply(ctx, nil); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	out, err := exec.Command("nft", "list", "table", "inet", "xui_speed").CombinedOutput()
	if err == nil {
		t.Fatalf("table still present after teardown:\n%s", out)
	}
}

func measureUpload(t *testing.T, addr string, mark int, n int) time.Duration {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer conn.Close()
	if mark != 0 {
		raw, err := conn.(*net.TCPConn).SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		var soerr error
		if err := raw.Control(func(fd uintptr) {
			soerr = setMark(fd, mark)
		}); err != nil || soerr != nil {
			t.Fatalf("SO_MARK: %v %v", err, soerr)
		}
	}
	buf := make([]byte, n)
	start := time.Now()
	if _, err := conn.Write(buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.(*net.TCPConn).CloseWrite()
	_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	tmp := make([]byte, 1)
	_, _ = conn.Read(tmp)
	return time.Since(start)
}

func measureDownload(t *testing.T, addr string, mark int, n int) time.Duration {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer conn.Close()
	raw, err := conn.(*net.TCPConn).SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var soerr error
	if err := raw.Control(func(fd uintptr) {
		soerr = setMark(fd, mark)
	}); err != nil || soerr != nil {
		t.Fatalf("SO_MARK: %v %v", err, soerr)
	}
	// One byte tells the peer to reply with n bytes on this connection.
	if _, err := conn.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
	start := time.Now()
	got := 0
	buf := make([]byte, 32*1024)
	for got < n {
		k, err := conn.Read(buf)
		got += k
		if err != nil {
			t.Fatalf("read %d/%d: %v", got, n, err)
		}
	}
	return time.Since(start)
}

func setMark(fd uintptr, mark int) error {
	return syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_MARK, mark)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

const peerScript = `
import os, socket
port = int(os.environ["PORT"])
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("10.99.8.2", port))
s.listen(8)
while True:
    c, _ = s.accept()
    data = b""
    c.settimeout(8)
    try:
        while True:
            b = c.recv(65536)
            if not b:
                break
            data += b
            if data[:1] == b"\x01" and len(data) == 1:
                c.sendall(b"z" * 256 * 1024)
                break
    except Exception:
        pass
    c.close()
`
