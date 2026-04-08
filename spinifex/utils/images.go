package utils

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Helper functions for OS images

var ErrQCOWDetected = errors.New("qcow format detected")

type Images struct {
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Distro       string    `json:"distro"`
	Version      string    `json:"version"`
	Arch         string    `json:"arch"`
	Platform     string    `json:"platform"`
	CreatedAt    time.Time `json:"created_at"`
	URL          string    `json:"url"`
	Checksum     string    `json:"checksum"`
	ChecksumType string    `json:"checksum_type"`
	BootMode     string    `json:"boot_mode"`
	Starred      bool      `json:"starred"`
}

var AvailableImages = map[string]Images{

	"debian-12-x86_64": {
		Name:         "debian-12-x86_64",
		Description:  "Debian 12 (Bookworm) x86_64 cloud image (generic cloud)",
		Distro:       "debian",
		Version:      "12",
		Arch:         "x86_64",
		Platform:     "Linux/UNIX",
		CreatedAt:    time.Date(2025, 10, 6, 0, 0, 0, 0, time.UTC),
		URL:          "https://cdimage.debian.org/cdimage/cloud/bookworm/latest/debian-12-generic-amd64.tar.xz",
		Checksum:     "https://cdimage.debian.org/cdimage/cloud/bookworm/latest/SHA512SUMS",
		ChecksumType: "sha512",
		BootMode:     "bios",
		Starred:      true,
	},

	"debian-12-arm64": {
		Name:         "debian-12-arm64",
		Description:  "Debian 12 (Bookworm) arm64 cloud image (generic cloud)",
		Distro:       "debian",
		Version:      "12",
		Arch:         "arm64",
		Platform:     "Linux/UNIX",
		CreatedAt:    time.Date(2025, 10, 6, 0, 0, 0, 0, time.UTC),
		URL:          "https://cdimage.debian.org/cdimage/cloud/bookworm/latest/debian-12-generic-arm64.tar.xz",
		Checksum:     "https://cdimage.debian.org/cdimage/cloud/bookworm/latest/SHA512SUMS",
		ChecksumType: "sha512",
		BootMode:     "bios",
		Starred:      true,
	},

	// Ubuntu
	"ubuntu-24.04-x86_64": {
		//Ubuntu 24.04 LTS (Noble Numbat)
		Name:         "ubuntu-24.04-x86_64",
		Description:  "Ubuntu 24.04 LTS (Noble Numbat) x86_64 cloud image",
		Distro:       "ubuntu",
		Version:      "24.04",
		Arch:         "x86_64",
		Platform:     "Linux/UNIX",
		CreatedAt:    time.Date(2025, 10, 6, 0, 0, 0, 0, time.UTC),
		URL:          "https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img",
		Checksum:     "https://cloud-images.ubuntu.com/noble/current/SHA256SUMS",
		ChecksumType: "sha256",
		BootMode:     "bios",
		Starred:      false,
	},

	"ubuntu-24.04-arm64": {
		//Ubuntu 24.04 LTS (Noble Numbat)
		Name:         "ubuntu-24.04-arm64",
		Description:  "Ubuntu 24.04 LTS (Noble Numbat) arm64 cloud image",
		Distro:       "ubuntu",
		Version:      "24.04",
		Arch:         "arm64",
		Platform:     "Linux/UNIX",
		CreatedAt:    time.Date(2025, 10, 6, 0, 0, 0, 0, time.UTC),
		URL:          "https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-arm64.img",
		Checksum:     "https://cloud-images.ubuntu.com/noble/current/SHA256SUMS",
		ChecksumType: "sha256",
		BootMode:     "bios",
		Starred:      false,
	},

	"lb-alpine-3.21.6-x86_64":
	// Alpine Linux (cloud init) x86_64 — LB system image with HAProxy and lb-agent
	{
		Name:         "lb-alpine-3.21.6-x86_64",
		Description:  "LB Alpine Linux 3.21.6 x86_64 system image",
		Distro:       "alpine",
		Version:      "3.21.6",
		Arch:         "x86_64",
		Platform:     "Linux/UNIX",
		CreatedAt:    time.Date(2026, 03, 27, 0, 0, 0, 0, time.UTC),
		URL:          "https://d2yp8ipz5jfqcw.cloudfront.net/alb-alpine-3.21.6-x86_64.raw",
		Checksum:     "https://d2yp8ipz5jfqcw.cloudfront.net/alb-alpine-3.21.6-x86_64.raw.sha512",
		ChecksumType: "sha512",
		BootMode:     "bios",
		Starred:      false,
	},

	/*
		"alpine-3.22.2-arm64":
		// Alpine Linux (cloud init) arm64 (Requires UEFI boot, TODO)
		{
			Name:         "alpine-3.22.2-arm64",
			Description:  "Alpine Linux 3.22.2 arm64 cloud image",
			Distro:       "alpine",
			Version:      "3.22.2",
			Arch:         "arm64",
			Platform:     "Linux/UNIX",
			CreatedAt:    time.Date(2025, 10, 6, 0, 0, 0, 0, time.UTC),
			URL:          "https://dl-cdn.alpinelinux.org/alpine/v3.22/releases/cloud/gcp_alpine-3.22.2-aarch64-uefi-cloudinit-metal-r0.raw.tar.gz",
			Checksum:     "https://dl-cdn.alpinelinux.org/alpine/v3.22/releases/cloud/gcp_alpine-3.22.2-aarch64-uefi-cloudinit-metal-r0.raw.tar.gz.sha512",
			ChecksumType: "sha512",
			BootMode:     "uefi",
			Starred:      false,
		},
	*/

}

// AMI / image extraction utils
func ExtractDiskImageFromFile(imagepath string, tmpdir string) (diskimage string, err error) {
	var args []string
	var execCmd string

	// Confirm file exists
	_, err = os.Stat(imagepath)

	if err != nil {
		return diskimage, err
	}

	// Extract the filepath
	imagefile := filepath.Base(imagepath)

	// Already in raw/image formt, confirm the file contains a valid disk image/MBR
	if strings.HasSuffix(imagefile, ".raw") || strings.HasSuffix(imagefile, ".img") || strings.HasSuffix(imagefile, ".qcow2") || strings.HasSuffix(imagefile, ".qcow") {
		path, err := filepath.Abs(imagepath)

		if err != nil {
			return path, err
		}

		// Validate the specified filename is indeed a disk image / MBR
		err = validateDiskImagePath(path)

		// Check error response

		if errors.Is(err, ErrQCOWDetected) {
			extractpath := fmt.Sprintf("%s/%s", tmpdir, imagefile)
			extractpath = strings.TrimSuffix(extractpath, ".qcow2") + ".raw"

			args = []string{
				"convert",
				"-f",
				"qcow2",
				"-O",
				"raw",
				imagepath,
				"-C",
				extractpath,
			}

			execCmd = "qemu-img"

			cmd := exec.Command(execCmd, args...)
			_, err = cmd.Output()

			if err != nil {
				return path, err
			}

			return extractpath, nil
		}

		return path, err
	} else if strings.HasSuffix(imagefile, ".tar.xz") {
		args = []string{
			"xfvJ",
			imagepath,
			"-C",
			tmpdir,
		}

		execCmd = "tar"
	} else if strings.HasSuffix(imagefile, ".tar.gz") || strings.HasSuffix(imagefile, ".tgz") {
		args = []string{
			"xfvz",
			imagepath,
			"-C",
			tmpdir,
		}

		execCmd = "tar"
	} else if strings.HasSuffix(imagefile, ".tar") {
		args = []string{
			"xfv",
			imagepath,
			"-C",
			tmpdir,
		}

		execCmd = "tar"
	} else if strings.HasSuffix(imagefile, ".xz") {
		args = []string{
			"-dk",
			imagepath,
		}

		execCmd = "xz"
	} else {
		err = errors.New("unsupported filetype")
		return diskimage, err
	}

	cmd := exec.Command(execCmd, args...)
	output, err := cmd.CombinedOutput()

	if err != nil {
		return diskimage, err
	}

	diskimage, err = extractDiskImagePath(tmpdir, output)

	return diskimage, err
}

func extractDiskImagePath(imagedir string, output []byte) (diskimage string, err error) {
	reader := bytes.NewReader(output)

	r := bufio.NewReader(reader)

	for {
		line, readErr := r.ReadString('\n')
		line = strings.TrimRight(line, "\n")

		// MacOS tar, filenames begin with `x FILE` (to STDERR)
		if runtime.GOOS == "darwin" && strings.HasPrefix(line, "x ") {
			line = strings.Replace(line, "x ", "", 1)
		}

		if strings.HasSuffix(line, ".raw") || strings.HasSuffix(line, ".img") {
			diskimage := fmt.Sprintf("%s/%s", imagedir, line)
			err = validateDiskImagePath(diskimage)
			return diskimage, err
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return "", fmt.Errorf("read tar output: %w", readErr)
		}
	}

	return diskimage, err
}

func validateDiskImagePath(diskimage string) (err error) {
	args := []string{
		diskimage,
	}

	cmd := exec.Command("file", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run file command on %s: %w", diskimage, err)
	}

	filetype := strings.Split(string(output), ":")

	if len(filetype) > 1 {
		if strings.Contains(filetype[1], "DOS/MBR boot sector") || strings.Contains(filetype[1], "Linux ") {
			return nil
		} else if strings.Contains(filetype[1], "QEMU QCOW") {
			return ErrQCOWDetected
		}
	}

	return errors.New("no valid disk image found")
}
