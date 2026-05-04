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
// The image is exactly 2 GiB (GiB-aligned, satisfying the GCE disk import requirement) and large
// enough to hold any bzImage.
func CreateKernelDiskImage(bzImagePath, outputPath string) error {
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

	// Build a 2 GiB ext2 image from the staging directory.
	// genext2fs is fully unprivileged — no loop devices, no mount calls.
	// 2 GiB = 2<<20 1-KiB blocks; GiB-aligned so no separate truncate is needed for GCE.
	const sizeBlocks = 2 << 20
	cmd := exec.Command("genext2fs",
		"-b", fmt.Sprintf("%d", sizeBlocks),
		"-d", stagingDir,
		outputPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("genext2fs failed: %w\n%s", err, out)
	}

	return nil
}
