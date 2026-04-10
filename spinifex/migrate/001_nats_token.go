package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

func init() {
	DefaultRegistry.RegisterConfigTarget("nats.conf", "nats/nats.conf", &NATSConfVersionReader{})

	DefaultRegistry.RegisterConfig("nats.conf", ConfigMigration{
		FromVersion: 0,
		ToVersion:   1,
		Description: "Enable NATS auth token and relocate log_file to /var/log/spinifex",
		Run: func(ctx ConfigContext) error {
			path := filepath.Join(ctx.ConfigDir, "nats", "nats.conf")
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			updated := content
			var changed bool

			// 1. Uncomment the token line inside the authorization block.
			// Matches "#  token:" with any leading/interstitial whitespace.
			commentedRe := regexp.MustCompile(`(?m)^(\s*)#(\s*token:\s*)`)
			uncommentedRe := regexp.MustCompile(`(?m)^\s*token:`)

			if commentedRe.Match(updated) {
				updated = commentedRe.ReplaceAll(updated, []byte("${1}${2}"))
				changed = true
			} else if !uncommentedRe.Match(updated) {
				return fmt.Errorf("nats.conf migration failed: token line not found (neither commented nor uncommented)")
			}

			// 2. Convert cluster route URLs from nats:// to nats-route://token@.
			// Existing deployments have: "nats://host:port"
			// Need:                      "nats-route://TOKEN@host:port"
			// Extract the token value from the (now uncommented) token line.
			tokenValueRe := regexp.MustCompile(`(?m)^\s*token:\s*"([^"]+)"`)
			tokenMatch := tokenValueRe.FindSubmatch(updated)

			if tokenMatch != nil {
				token := string(tokenMatch[1])
				// Match route lines: "nats://host:port" (without token already embedded).
				routeRe := regexp.MustCompile(`"nats://([^@"]+)"`)
				if routeRe.Match(updated) {
					replacement := fmt.Sprintf(`"nats-route://%s@${1}"`, token)
					updated = routeRe.ReplaceAll(updated, []byte(replacement))
					changed = true
				}
			}

			// 3. Relocate log_file from data dir to /var/log/spinifex.
			// v1.0.2 rendered: log_file: "/var/lib/spinifex/logs/nats.log"
			// New location:    log_file: "/var/log/spinifex/nats.log"
			// The new systemd unit sandbox only permits writes under /var/log/spinifex,
			// so leaving the old path causes nats to crash-loop on "permission denied".
			oldLogRe := regexp.MustCompile(`(?m)^log_file:\s*"/var/lib/spinifex/logs/nats\.log"`)
			newLogLine := `log_file: "/var/log/spinifex/nats.log"`
			if oldLogRe.Match(updated) {
				updated = oldLogRe.ReplaceAll(updated, []byte(newLogLine))
				changed = true
			}

			if !changed {
				ctx.Logger.Info("nats.conf: already migrated, skipping")
				return nil
			}

			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			if err := os.WriteFile(path, updated, info.Mode()); err != nil {
				return err
			}

			// Post-migration validation.
			result, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("nats.conf migration: post-migration validation read failed: %w", err)
			}
			if !uncommentedRe.Match(result) {
				return fmt.Errorf("nats.conf migration failed: token line not found after uncommenting")
			}
			// Validate log_file no longer points at the old data-dir location.
			if oldLogRe.Match(result) {
				return fmt.Errorf("nats.conf migration failed: log_file still points to /var/lib/spinifex/logs after rewrite")
			}
			return nil
		},
	})
}
