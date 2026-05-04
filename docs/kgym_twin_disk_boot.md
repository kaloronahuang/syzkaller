# kGymSuite: Twin-Disk Boot (Per-Job Kernel Injection)

This document describes the twin-disk boot feature added to the kGymSuite fork of
syzkaller. It allows syz-crush to run a specific kernel (`bzImage`) without
modifying the base userspace disk image, by attaching the kernel as a second
disk that GRUB loads at boot time.

## Background

The standard syzkaller GCE backend requires the kernel to be embedded inside
the boot disk image. In kGymSuite, `kbuilder` produces a fresh `bzImage` for
every job. Embedding it into the userspace image requires privileged loop-device
operations (`losetup`), which conflicts with unprivileged Kubernetes pod
deployment.

The twin-disk approach solves this by splitting responsibilities:

| Disk | Content | Owner |
|------|---------|-------|
| Disk 0 (boot) | Userspace OS + GRUB (pre-built, never mutated) | base image |
| Disk 1 (kernel) | Only `bzImage`, in a tiny ext2 filesystem | created per-job |

GRUB on disk 0 is pre-configured to load the kernel from `(hd1)/bzImage`.

---

## Prerequisites

### 1. Install `genext2fs`

The kernel disk image is built with
[`genext2fs`](https://genext2fs.sourceforge.net/), which creates ext2 filesystem
images entirely in userspace with no kernel interfaces or elevated privileges.

```bash
# Debian/Ubuntu
sudo apt install genext2fs

# From source
git clone https://github.com/bestouff/genext2fs && cd genext2fs
./autogen.sh && ./configure && make && sudo make install
```

### 2. Update the base userspace image (one-time)

The boot disk's GRUB configuration must be changed to load the kernel from the
second disk instead of the first. This is a **one-time offline change** to the
base image.

Boot into (or chroot into) the userspace image and edit `/boot/grub/grub.cfg`
(or the equivalent file for your GRUB setup):

```
set default=0
set timeout=0

menuentry "syzkaller" {
    set root=(hd1)
    linux /bzImage root=/dev/sda console=ttyS0 earlyprintk=serial oops=panic panic_on_warn=1 panic=86400
    boot
}
```

Key points:
- `set root=(hd1)` — tells GRUB to look for the kernel on the second disk.
- `root=/dev/sda` in the kernel cmdline still refers to the userspace filesystem
  on the first disk (disk 0 = `/dev/sda`). The `set root` directive is for GRUB
  only, not for the running kernel.
- The same `grub.cfg` works for both GCE (SCSI) and QEMU (IDE) because both
  present the second disk as `(hd1)` to GRUB.

After editing, rebuild GRUB or just save the config file (if GRUB reads it
directly at boot). Re-package the image as needed for your pipeline.

---

## GCE Configuration

For GCE-backed syz-crush runs, set `kernel_image` and `gcs_bucket` in the VM
section of the syz-crush config:

```json
{
  "name": "kgym-crush",
  "target": "linux/amd64",
  "http": "0.0.0.0:56741",
  "workdir": "/tmp/syzkaller-workdir",
  "kernel_obj": "/path/to/kernel-build",
  "image": "/dev/null",
  "sshkey": "/path/to/ssh-key",
  "syzkaller": "/path/to/syzkaller",
  "vm": {
    "type": "gce",
    "gce_image":    "my-userspace-v3",
    "kernel_image": "/path/to/bzImage",
    "gcs_bucket":   "my-kernels-bucket",
    "machine_type": "n1-standard-2",
    "count": 4,
    "preemptible": true
  }
}
```

### What happens at startup

1. syz-crush computes `SHA256(bzImage)[:16]` to form a kernel image name:
   `<pool-name>-kernel-<hash16>`.
2. If a GCE image with that name already exists, it is **reused** (cache hit —
   the same kernel binary was used in a previous run).
3. Otherwise:
   - A 2 GiB ext2 filesystem image is created with `genext2fs`, containing
     only `bzImage` at the root. 2 GiB is GiB-aligned (satisfying the GCE
     disk import requirement) and large enough for any bzImage.
   - It is wrapped as `disk.raw` inside a gzip-compressed tar archive and
     uploaded to `gs://<gcs_bucket>/syzkaller-kernels/<name>.tar.gz`.
   - GCE imports it as a new image.
   - The GCS tar.gz blob is **immediately deleted** after import.
4. Each VM instance is created with two disks:
   - Boot disk: initialized from `gce_image` (the pre-built userspace image).
   - Kernel disk: initialized from the imported kernel GCE image, attached as
     a non-boot persistent disk (`AutoDelete: true`).
5. When the pool is shut down, `pool.Close()` deletes the kernel GCE image.

### Config fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `gce_image` | string | yes | Name of the pre-created GCE boot disk image |
| `kernel_image` | string | no | Local path to `bzImage`; enables twin-disk boot |
| `gcs_bucket` | string | if `kernel_image` set | GCS bucket for kernel disk uploads |
| `gcs_path` | string | if `gce_image` not set | GCS path for boot image upload |
| `machine_type` | string | yes | GCE machine type |
| `count` | int | no | Number of VMs (default: 1) |
| `preemptible` | bool | no | Use preemptible VMs (default: true) |

---

## QEMU Configuration

For local QEMU-backed runs, set `kernel_disk` in the VM section. The primary
image can be provided as a `.tar.gz` archive (it will be automatically
extracted):

```json
{
  "name": "kgym-crush-local",
  "target": "linux/amd64",
  "http": "0.0.0.0:56741",
  "workdir": "/tmp/syzkaller-workdir",
  "kernel_obj": "/path/to/kernel-build",
  "image": "/path/to/image.tar.gz",
  "sshkey": "/path/to/ssh-key",
  "syzkaller": "/path/to/syzkaller",
  "vm": {
    "type": "qemu",
    "kernel_disk": "/path/to/bzImage",
    "cpu": 2,
    "mem": 2048,
    "count": 1
  }
}
```

### What happens per VM instance

1. If `image` ends with `.tar.gz`, `disk.raw` is extracted from the archive
   into the instance workdir and used as the primary QEMU disk.
2. A 2 GiB ext2 image containing only `bzImage` is created with `genext2fs`
   in the instance workdir.
3. QEMU is started with two disks:
   - Primary (IDE index 0): the userspace disk image (`-hda` / `image_device`).
   - Kernel (IDE index 1): the tiny ext2 image
     (`-drive if=ide,index=1,format=raw,file=kernel.img`).
4. Both files are cleaned up automatically when the workdir is removed on
   instance close.

### Config fields

| Field | Type | Description |
|-------|------|-------------|
| `kernel_disk` | string | Local path to `bzImage`; enables twin-disk boot via second IDE drive |
| `kernel` | string | **Mutually exclusive with `kernel_disk`**. Passes `-kernel` to QEMU directly (injected boot, no GRUB). |

> **Note:** `kernel_disk` and `kernel` cannot be set at the same time.

---

## Architecture Diagram

```
┌───────────────────────────────────────────────┐
│  syz-crush / kvmmanager                       │
│                                               │
│  ┌─────────────┐     ┌──────────────────────┐ │
│  │  bzImage    │────▶│  genext2fs           │ │
│  │  (per-job)  │     │  → 2 GiB ext2        │ │
│  └─────────────┘     └──────────┬───────────┘ │
│                                 │             │
│             GCE: upload + import as GCE image │
│             QEMU: use local file directly     │
│                                 │             │
│  ┌──────────────────────────────▼──────────┐  │
│  │  VM instance                            │  │
│  │                                         │  │
│  │  Disk 0 (boot): userspace + GRUB        │  │
│  │  Disk 1 (kernel): bzImage (hd1)         │  │
│  │                                         │  │
│  │  GRUB:  set root=(hd1)                  │  │
│  │         linux /bzImage root=/dev/sda    │  │
│  └─────────────────────────────────────────┘  │
└───────────────────────────────────────────────┘
```

---

## Caching (GCE only)

The kernel GCE image name encodes the SHA256 of the bzImage (first 16 hex
characters):

```
<pool-name>-kernel-<sha256[:16]>
```

If an image with this name already exists in the project, the upload/import
pipeline is skipped entirely. This means repeated runs with the same kernel
build are fast — no upload, no GCS object, no import wait.

The image is deleted by `pool.Close()` when syz-crush exits. If the process
crashes before cleanup, the orphaned image can be deleted manually:

```bash
gcloud compute images delete <pool-name>-kernel-<hash> --project <project>
```

---

## Troubleshooting

**VM boots but panics immediately / kernel not found**

- Confirm the base image `grub.cfg` has `set root=(hd1)` (not `(hd0)`).
- For GCE: verify the kernel disk was attached. Check instance details:
  ```bash
  gcloud compute instances describe <instance-name> --format='json(disks)'
  ```
  You should see two disks, with the second one having `boot: false`.
- For QEMU: add `-drive if=ide,index=1,...` to the QEMU args manually to verify
  GRUB can see it.

**`genext2fs: command not found`**

Install `genext2fs` (see Prerequisites above).

**GCE import fails with "disk.raw must be a multiple of 1 GB"**

This should not happen with the built-in padding (`truncate -s 1073741824`).
If it does, verify the truncate step completed before the upload.

**Cache not being hit despite same kernel**

The cache key is the SHA256 of the bzImage file. If the build system produces
a different binary (e.g. embedded timestamps), the hash will differ. Check with:
```bash
sha256sum bzImage | cut -c1-16
```
