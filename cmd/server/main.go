package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/dongsheng123132/remote-agent/internal/server"
)

func main() {
	port := flag.Int("port", 8900, "server listen port")
	agentToken := flag.String("agent-token", "", "token for agent connections (public)")
	controllerToken := flag.String("controller-token", "", "token for controller connections (secret)")
	tlsCert := flag.String("tls-cert", "", "TLS certificate file path")
	tlsKey := flag.String("tls-key", "", "TLS private key file path")
	requireKeys := flag.Bool("require-device-keys", false, "require v0.2 Ed25519 device keys + pairing codes")
	flag.Parse()

	// Prefer environment variables over flags (avoids token in process list)
	if env := os.Getenv("AGENT_TOKEN"); env != "" {
		*agentToken = env
	}
	if env := os.Getenv("CONTROLLER_TOKEN"); env != "" {
		*controllerToken = env
	}
	if env := os.Getenv("TLS_CERT"); env != "" {
		*tlsCert = env
	}
	if env := os.Getenv("TLS_KEY"); env != "" {
		*tlsKey = env
	}
	if *agentToken == "" {
		*agentToken = "REPLACE_WITH_AGENT_TOKEN"
	}
	if *controllerToken == "" {
		log.Fatal("controller token required: set CONTROLLER_TOKEN env or --controller-token flag")
	}
	if env := os.Getenv("REQUIRE_DEVICE_KEYS"); env != "" {
		v, err := strconv.ParseBool(env)
		if err != nil {
			log.Fatalf("REQUIRE_DEVICE_KEYS must be true/false: %v", err)
		}
		*requireKeys = v
	}

	hub := server.NewHub(*agentToken, *controllerToken)
	hub.SetRequireDeviceKeys(*requireKeys)
	if *requireKeys {
		log.Printf("Device identity enforcement: ON (Ed25519 key + one-time pairing code required)")
	} else {
		log.Printf("Device identity enforcement: OFF (legacy agents accepted; set REQUIRE_DEVICE_KEYS=1 to enforce)")
	}
	handler := server.NewHandler(hub)

	http.HandleFunc("/ws/agent", handler.HandleAgent)
	http.HandleFunc("/ws/controller", handler.HandleController)

	// Health check
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	addr := fmt.Sprintf(":%d", *port)

	if *tlsCert != "" && *tlsKey != "" {
		log.Printf("Relay server starting on %s (TLS)", addr)
		log.Printf("  Agent endpoint:      wss://localhost%s/ws/agent", addr)
		log.Printf("  Controller endpoint: wss://localhost%s/ws/controller", addr)
		if err := http.ListenAndServeTLS(addr, *tlsCert, *tlsKey, nil); err != nil {
			log.Fatalf("Server failed: %v", err)
		}
	} else {
		log.Printf("Relay server starting on %s (no TLS)", addr)
		log.Printf("  Agent endpoint:      ws://localhost%s/ws/agent", addr)
		log.Printf("  Controller endpoint: ws://localhost%s/ws/controller", addr)
		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Fatalf("Server failed: %v", err)
		}
	}
}
