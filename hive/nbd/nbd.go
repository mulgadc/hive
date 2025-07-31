package nbd

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

type NBDKitConfig struct {
	Port       int    `json:"port"`
	PidFile    string `json:"pid_file"`
	PluginPath string `json:"plugin_path"`
	Verbose    bool   `json:"verbose"`
	Foreground bool   `json:"foreground"`
	Size       int64  `json:"size"`
	Volume     string `json:"volume"`
	Bucket     string `json:"bucket"`
	Region     string `json:"region"`
	AccessKey  string `json:"access_key"`
	SecretKey  string `json:"secret_key"`
	BaseDir    string `json:"base_dir"`
	Host       string `json:"host"`
	CacheSize  int    `json:"cache_size"`
}

func (cfg *NBDKitConfig) Execute() (*exec.Cmd, error) {
	args := []string{
		"-f", // foreground required for Golang plugin via nbdkit
		"-p", strconv.Itoa(cfg.Port),
		"--pidfile", cfg.PidFile,
		cfg.PluginPath,
	}

	if cfg.Verbose {
		args = append(args, "-v")
	}

	// Add plugin-specific arguments
	pluginArgs := []string{
		fmt.Sprintf("size=%d", cfg.Size),
		fmt.Sprintf("volume=%s", cfg.Volume),
		fmt.Sprintf("bucket=%s", cfg.Bucket),
		fmt.Sprintf("region=%s", cfg.Region),
		fmt.Sprintf("access_key=%s", cfg.AccessKey),
		fmt.Sprintf("secret_key=%s", cfg.SecretKey),
		fmt.Sprintf("base_dir=%s", cfg.BaseDir),
		fmt.Sprintf("host=%s", cfg.Host),
		fmt.Sprintf("cache_size=%d", cfg.CacheSize),
	}

	args = append(args, pluginArgs...)

	cmd := exec.Command("nbdkit", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd, cmd.Start()
}
