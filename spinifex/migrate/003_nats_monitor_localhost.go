package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

func init() {
	DefaultRegistry.RegisterConfig("nats.conf", ConfigMigration{
		FromVersion: 1,
		ToVersion:   2,
		Description: "Bind NATS monitoring to localhost only",
		Run: func(ctx ConfigContext) error {
			path := filepath.Join(ctx.ConfigDir, "nats", "nats.conf")
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			// Match "http: <anything>:8222" or "http_port: 8222"
			httpRe := regexp.MustCompile(`(?m)^http(?:_port)?:\s*.*8222\s*$`)
			if !httpRe.Match(content) {
				ctx.Logger.Info("nats.conf: no monitoring line found, skipping")
				return nil
			}

			updated := httpRe.ReplaceAll(content, []byte("http: 127.0.0.1:8222"))

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
				return fmt.Errorf("post-migration validation read failed: %w", err)
			}
			localhostRe := regexp.MustCompile(`(?m)^http:\s*127\.0\.0\.1:8222\s*$`)
			if !localhostRe.Match(result) {
				return fmt.Errorf("monitoring line not rewritten to localhost after migration")
			}
			return nil
		},
	})
}
