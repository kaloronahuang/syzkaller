// Copyright 2024 syzkaller project authors. All rights reserved.
// Use of this source code is governed by Apache 2 LICENSE that can be found in the LICENSE file.

package vmimpl

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// CreateKernelDiskImage builds a raw ext2 filesystem image containing only bzImage at its root.
// The image is created using genext2fs, which requires no kernel interfaces or elevated privileges.
// If pad is true, the image is truncated to exactly 1 GiB (required for GCE image import).
func CreateKernelDiskImage(bzImagePath, outputPath string, pad bool) error {
	// Create a temporary staging directory with just the bzImage inside.
	stagingDir, err := os.MkdirTemp("", "kerneldisk-*")
	if err != nil {
		return fmt.Errorf("failed to create staging dir: %w", err)
	}
	defer os.RemoveAll(stagingDir)

	dst := filepath.Join(stagingDir, "bzImage")
	data, err := os.ReadFile(bzImagePath)
	if err != nil {
		return fmt.Errorf("failed to read bzImage: %w", err)
	}
	if err := os.WriteFile(dst, data, 0644); err != nil {
		return fmt.Errorf("failed to write bzImage to staging dir: %w", err)
	}

	// Build a 64 MiB ext2 image from the staging directory.
	// genext2fs is fully unprivileged — no loop devices, no mount calls.
	const sizekb = 65536 // 64 MiB
	cmd := exec.Command("genext2fs",
		"-b", fmt.Sprintf("%d", sizekb),
		"-d", stagingDir,
		outputPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("genext2fs failed: %w\n%s", err, out)
	}

	// GCE requires disk images to be aligned to a 1 GiB boundary.
	if pad {
		const oneGiB = int64(1 << 30)
		if err := os.Truncate(outputPath, oneGiB); err != nil {
			return fmt.Errorf("failed to pad kernel disk image to 1 GiB: %w", err)
		}
	}

	return nil
}
