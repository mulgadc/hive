package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func init() {
	// nats.conf: version 2 → 3 — add TLS blocks for client and cluster connections.
	DefaultRegistry.RegisterConfig("nats.conf", ConfigMigration{
		FromVersion: 2,
		ToVersion:   3,
		Description: "Add TLS for client connections and mutual TLS for cluster routes",
		Run: func(ctx ConfigContext) error {
			path := filepath.Join(ctx.ConfigDir, "nats", "nats.conf")
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			text := string(content)

			// Skip if TLS is already configured.
			if strings.Contains(text, "tls {") {
				ctx.Logger.Info("nats.conf: TLS block already present, skipping")
				return nil
			}

			certFile := fmt.Sprintf("%s/server.pem", ctx.ConfigDir)
			keyFile := fmt.Sprintf("%s/server.key", ctx.ConfigDir)
			caFile := fmt.Sprintf("%s/ca.pem", ctx.ConfigDir)

			// 1. Inject client TLS block after the listen line.
			clientTLS := fmt.Sprintf("\n\ntls {\n  cert_file: \"%s\"\n  key_file:  \"%s\"\n  ca_file:   \"%s\"\n}", certFile, keyFile, caFile)

			listenRe := regexp.MustCompile(`(?m)^listen:\s*.+$`)
			if !listenRe.MatchString(text) {
				return fmt.Errorf("nats.conf: listen line not found")
			}
			text = listenRe.ReplaceAllStringFunc(text, func(match string) string {
				return match + clientTLS
			})

			// 2. Inject cluster TLS block inside the cluster {} block, after the listen line.
			clusterTLS := fmt.Sprintf("\n\n  tls {\n    cert_file: \"%s\"\n    key_file:  \"%s\"\n    ca_file:   \"%s\"\n    verify:    true\n  }", certFile, keyFile, caFile)

			// Match the cluster listen line and insert TLS block after it.
			clusterListenRe := regexp.MustCompile(`(?m)^(\s*listen:\s*\S+:4248)\s*$`)
			if clusterListenRe.MatchString(text) {
				text = clusterListenRe.ReplaceAllStringFunc(text, func(match string) string {
					return strings.TrimRight(match, "\n") + clusterTLS
				})
			}

			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			return os.WriteFile(path, []byte(text), info.Mode())
		},
	})

	// spinifex.toml: version 1 → 2 — add cacert under [nodes.*.nats].
	DefaultRegistry.RegisterConfigTarget("spinifex.toml", "spinifex.toml", &TOMLVersionReader{})

	DefaultRegistry.RegisterConfig("spinifex.toml", ConfigMigration{
		FromVersion: 1,
		ToVersion:   2,
		Description: "Add NATS CA cert path for TLS",
		Run: func(ctx ConfigContext) error {
			path := filepath.Join(ctx.ConfigDir, "spinifex.toml")
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			text := string(content)

			// Skip if cacert is already present.
			if strings.Contains(text, "cacert") {
				ctx.Logger.Info("spinifex.toml: cacert already present, skipping")
				return nil
			}

			// Insert cacert after each host line under [nodes.*.nats].
			// Pattern: host = "x.x.x.x:4222" followed by a newline.
			caPath := fmt.Sprintf("%s/ca.pem", ctx.ConfigDir)
			natsHostRe := regexp.MustCompile(`(?m)^(host\s*=\s*"[^"]+:4222")\s*$`)
			text = natsHostRe.ReplaceAllStringFunc(text, func(match string) string {
				return strings.TrimRight(match, "\n") + fmt.Sprintf("\ncacert = \"%s\"", caPath)
			})

			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			return os.WriteFile(path, []byte(text), info.Mode())
		},
	})

	// predastore.toml: version 2 → 3 — add nats_ca_cert under [iam].
	DefaultRegistry.RegisterConfig("predastore.toml", ConfigMigration{
		FromVersion: 2,
		ToVersion:   3,
		Description: "Add NATS CA cert path for TLS",
		Run: func(ctx ConfigContext) error {
			path := filepath.Join(ctx.ConfigDir, "predastore", "predastore.toml")
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			text := string(content)

			// Skip if nats_ca_cert is already present.
			if strings.Contains(text, "nats_ca_cert") {
				ctx.Logger.Info("predastore.toml: nats_ca_cert already present, skipping")
				return nil
			}

			// Insert nats_ca_cert after the nats_token line.
			caPath := fmt.Sprintf("%s/ca.pem", ctx.ConfigDir)
			natsTokenRe := regexp.MustCompile(`(?m)^(nats_token\s*=\s*"[^"]*")\s*$`)
			text = natsTokenRe.ReplaceAllStringFunc(text, func(match string) string {
				return strings.TrimRight(match, "\n") + fmt.Sprintf("\nnats_ca_cert = \"%s\"", caPath)
			})

			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			return os.WriteFile(path, []byte(text), info.Mode())
		},
	})
}
