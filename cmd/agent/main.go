package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dongsheng123132/remote-agent/internal/agent"
)

func main() {
	// Force UTF-8 console output on Windows.  Without this, the chinese
	// audit-log banner renders as garbled CP936 ("å®¡è®¡æ¥å¿") in any
	// console whose code page hasn't been flipped to 65001 yet — and
	// agent.ps1's `chcp 65001` only affects the parent shell, not the
	// console our process inherits.
	setUTF8Console()

	serverAddr := flag.String("server", "ws://localhost:8900", "relay server address")
	token := flag.String("token", "REPLACE_WITH_AGENT_TOKEN", "auth token")
	deviceID := flag.String("id", "", "device ID (auto-generated if empty)")
	pairingCode := flag.String("pairing-code", os.Getenv("PAIRING_CODE"), "one-time pairing code for first registration (env: PAIRING_CODE)")
	keyFile := flag.String("key-file", os.Getenv("DEVICE_KEY_FILE"), "Ed25519 device key path (default ~/.uclaw/device.key)")
	fileRoots := flag.String("file-roots", os.Getenv("ULINK_FILE_ROOTS"), "comma-separated allowed dirs for push/pull (default: home + temp)")
	tlsPin := flag.String("tls-pin", os.Getenv("ULINK_TLS_PIN"), "relay TLS certificate SHA-256 fingerprint (self-signed relay)")
	flag.Parse()

	wsURL := fmt.Sprintf("%s/ws/agent", *serverAddr)
	log.Printf("Agent starting, server: %s", wsURL)

	client := agent.NewClient(wsURL, *token, *deviceID)
	if *pairingCode != "" {
		client.SetPairingCode(*pairingCode)
	}
	if *keyFile != "" {
		client.SetDeviceKeyPath(*keyFile)
	}
	if *fileRoots != "" {
		client.SetFileRoots(strings.Split(*fileRoots, ","))
	}
	if *tlsPin != "" {
		client.SetTLSPin(*tlsPin)
	}
	client.SetEnrollCallback(func(code string, expires time.Time) {
		fmt.Println()
		fmt.Printf("  请把下面这串验证码发给 IT：\n    %s\n", code)
		fmt.Printf("  （%s 前有效，只用于本次连接）\n", time.Until(expires).Round(time.Minute))
		copyToClipboard(code)
	})

	// Highlight the device ID up-front and copy it to the clipboard.
	// Why both: customers were misreading the ID off the console (e.g.
	// pc-3799 vs pc-3779) when copy-pasting into WeChat, and the
	// surrounding log lines made the ID hard to spot at a glance.
	// The clipboard copy means the very first Ctrl+V they do hands
	// us the right string.  No-op on non-Windows.
	id := client.DeviceID()
	const (
		clrReset  = "\x1b[0m"
		clrGreen  = "\x1b[1;32m"
		clrCyan   = "\x1b[1;36m"
		clrYellow = "\x1b[1;33m"
	)
	fmt.Println()
	fmt.Printf("%s  ╔══════════════════════════════════════╗%s\n", clrCyan, clrReset)
	fmt.Printf("%s  ║   请把下面这串 ID 发给运维:          ║%s\n", clrCyan, clrReset)
	fmt.Printf("%s  ╠══════════════════════════════════════╣%s\n", clrCyan, clrReset)
	fmt.Printf("%s  ║                                      ║%s\n", clrCyan, clrReset)
	fmt.Printf("%s  ║      ▶ %s%-20s%s              ║%s\n", clrCyan, clrGreen, id, clrCyan, clrReset)
	fmt.Printf("%s  ║                                      ║%s\n", clrCyan, clrReset)
	fmt.Printf("%s  ╚══════════════════════════════════════╝%s\n", clrCyan, clrReset)
	fmt.Printf("%s  (已自动复制到剪贴板，可直接 Ctrl+V 粘贴到微信)%s\n", clrYellow, clrReset)
	fmt.Println()
	copyToClipboard(id)

	// Banner: tell the customer where the audit log lives, so they can
	// open it later if they want to know what the operator ran.  This
	// is the lightweight "consent UI" we have today — a tray icon
	// would be nicer but ships several MB of GUI deps (systray + cgo)
	// and most customers see this console window briefly anyway when
	// they start the agent through agent.ps1.
	if path := client.AuditPath(); path != "" {
		fmt.Println("  本次远程协助会话审计日志:")
		fmt.Printf("    %s\n", path)
		fmt.Println("  (运维每次执行命令、推送文件、拉取文件都会记录在这里)")
		fmt.Println()
	}

	// Graceful shutdown so the audit log gets a "session end" line and
	// gets flushed.  Without this, customers closing the console window
	// (the most common shutdown path) would lose the trailing context.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		client.Close()
		os.Exit(0)
	}()

	client.Run()
}
