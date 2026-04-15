package migrate

import (
	"os"
	"path/filepath"
	"strings"
)

const awsgwRatelimitSection = `
# API request throttling (per-account, per-action token bucket)
[ratelimit]
enabled = false
rate = 20           # Default sustained requests per second per account per action
burst = 100         # Default maximum burst capacity

[ratelimit.action.RunInstances]
rate = 2
burst = 40

[ratelimit.action.TerminateInstances]
rate = 2
burst = 40

[ratelimit.action.StartInstances]
rate = 2
burst = 40

[ratelimit.action.StopInstances]
rate = 2
burst = 40
`

const predastoreRatelimitSection = `
# API request throttling (per-account, per-action token bucket)
[ratelimit]
enabled = false
rate = 100          # Default sustained requests per second per account per action
burst = 500         # Default maximum burst capacity

[ratelimit.action."s3:PutObject"]
rate = 50
burst = 200

[ratelimit.action."s3:DeleteObject"]
rate = 50
burst = 200
`

func init() {
	// awsgw.toml: version 1 → 2 — append [ratelimit] section.
	DefaultRegistry.RegisterConfigTarget("awsgw.toml", "awsgw/awsgw.toml", &TOMLVersionReader{})

	DefaultRegistry.RegisterConfig("awsgw.toml", ConfigMigration{
		FromVersion: 1,
		ToVersion:   2,
		Description: "Add [ratelimit] section for API request throttling",
		Run: func(ctx ConfigContext) error {
			return appendRatelimitSection(
				filepath.Join(ctx.ConfigDir, "awsgw", "awsgw.toml"),
				awsgwRatelimitSection,
			)
		},
	})

	// predastore.toml: version 1 → 2 — append [ratelimit] section.
	DefaultRegistry.RegisterConfigTarget("predastore.toml", "predastore/predastore.toml", &TOMLVersionReader{})

	DefaultRegistry.RegisterConfig("predastore.toml", ConfigMigration{
		FromVersion: 1,
		ToVersion:   2,
		Description: "Add [ratelimit] section for API request throttling",
		Run: func(ctx ConfigContext) error {
			return appendRatelimitSection(
				filepath.Join(ctx.ConfigDir, "predastore", "predastore.toml"),
				predastoreRatelimitSection,
			)
		},
	})
}

// appendRatelimitSection appends the ratelimit TOML section to a config file.
// Idempotent — skips if [ratelimit] is already present.
func appendRatelimitSection(path, section string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	if strings.Contains(string(data), "[ratelimit]") {
		return nil // already present
	}

	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	updated := append(data, []byte(section)...)
	return os.WriteFile(path, updated, info.Mode())
}
