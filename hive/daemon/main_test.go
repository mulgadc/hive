package daemon

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
)

// Shared NATS servers for all daemon tests — started once in TestMain.
var (
	sharedNATSServer   *server.Server
	sharedNATSURL      string
	sharedJSServer     *server.Server
	sharedJSNATSURL    string
)

func TestMain(m *testing.M) {
	// Start a plain NATS server (no JetStream)
	ns, err := server.NewServer(&server.Options{
		Host:   "127.0.0.1",
		Port:   -1,
		NoLog:  true,
		NoSigs: true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create shared NATS server: %v\n", err)
		os.Exit(1)
	}
	go ns.Start()
	if !ns.ReadyForConnections(5 * time.Second) {
		fmt.Fprintln(os.Stderr, "Shared NATS server failed to start")
		os.Exit(1)
	}
	sharedNATSServer = ns
	sharedNATSURL = ns.ClientURL()

	// Start a JetStream-enabled NATS server
	jsTmpDir, err := os.MkdirTemp("", "nats-js-shared-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create JetStream temp dir: %v\n", err)
		os.Exit(1)
	}
	jsNS, err := server.NewServer(&server.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		StoreDir:  jsTmpDir,
		NoLog:     true,
		NoSigs:    true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create shared JetStream NATS server: %v\n", err)
		os.Exit(1)
	}
	go jsNS.Start()
	if !jsNS.ReadyForConnections(5 * time.Second) {
		fmt.Fprintln(os.Stderr, "Shared JetStream NATS server failed to start")
		os.Exit(1)
	}
	sharedJSServer = jsNS
	sharedJSNATSURL = jsNS.ClientURL()

	code := m.Run()

	ns.Shutdown()
	jsNS.Shutdown()
	os.RemoveAll(jsTmpDir)

	os.Exit(code)
}
