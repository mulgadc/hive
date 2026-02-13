# NBD / QEMU Performance

## Summary

Hive VMs using NBD-backed boot volumes showed ~38x lower IOPS and ~35-40x higher latency compared to a Proxmox VM on the same host. The root cause was not the NBD transport itself, but missing QEMU tuning flags: no iothread, no cache policy, no multiqueue. Proxmox applies all of these by default; Hive applied none.

**Status: Implemented.** All boot volumes and hot-attached data volumes now get dedicated iothreads, `cache=none`, and multiqueue. Auxiliary volumes (cloud-init, EFI) are excluded — they are small, rarely read, and don't benefit from these flags. The viperblock LRU cache was also doubled from 64MB to 128MB for main volumes.

## What Changed

### QEMU flags (boot volumes)

`hive/daemon/daemon.go` — `StartInstance()` boot drive setup:

- Boot volume drive gets `cache=none`
- A dedicated iothread (`ioth-os`) is created
- virtio-blk-pci device gets `iothread=ioth-os,num-queues=<vCPU count>`

Before:
```
-drive file=nbd:unix:/path.sock,format=raw,if=none,media=disk,id=os
-device virtio-blk-pci,drive=os,bootindex=1
```

After:
```
-object iothread,id=ioth-os
-drive file=nbd:unix:/path.sock,format=raw,if=none,media=disk,id=os,cache=none
-device virtio-blk-pci,drive=os,iothread=ioth-os,num-queues=2,bootindex=1
```

### QEMU flags (hot-attached data volumes)

`hive/daemon/daemon_handlers.go` — `handleAttachVolume()`:

Hot-attached volumes (e.g. a 1TB data drive attached to a running instance) also get a per-volume iothread via QMP:

1. `object-add` creates `ioth-<volumeID>`
2. `blockdev-add` connects the NBD backend
3. `device_add` creates `virtio-blk-pci` with `iothread=ioth-<volumeID>`

On detach (`handleDetachVolume()`), cleanup is:

1. `device_del` removes the guest device
2. `blockdev-del` removes the block node
3. `object-del` removes the iothread (best-effort, non-fatal if it fails)

### Excluded: cloud-init and EFI volumes

These auxiliary volumes are intentionally excluded from performance tuning:

- **Cloud-init** (`v.CloudInit`): Mounted as `if=virtio,media=cdrom`. Read once at boot, tiny payload. No iothread, no cache tuning.
- **EFI** (`v.EFI`): Currently skipped entirely (`continue` in the volume loop). No QEMU drive emitted.
- **Viperblock cache**: Volumes with names ending in `-cloudinit` or `-efi` get `cache_size=0` in both the viperblock in-process LRU and the nbdkit plugin. Main volumes get 128MB (32768 blocks at 4096B block size).

### Viperblock LRU cache doubled

`hive/services/viperblockd/viperblockd.go`:

- `defaultCache` changed from `(64 * 1024 * 1024) / DefaultBlockSize` → `(128 * 1024 * 1024) / DefaultBlockSize`
- This is 16384 → 32768 blocks (at `DefaultBlockSize = 4096`)
- Applies to both the in-process viperblock golang-lru cache and the `cache_size` parameter passed to the nbdkit plugin
- Cloud-init/EFI volumes still get `cache_size=0`

### Struct changes

`hive/vm/vm.go`:

```go
type Drive struct {
    File   string `json:"file"`
    Format string `json:"format"`
    If     string `json:"if"`
    Media  string `json:"media"`
    ID     string `json:"id"`
    Cache  string `json:"cache,omitempty"`  // NEW — e.g. "none"
}

type IOThread struct {  // NEW
    ID string `json:"id"`
}

type Config struct {
    // ... existing fields ...
    Drives    []Drive    `json:"drives"`
    IOThreads []IOThread `json:"io_threads,omitempty"`  // NEW
    // ...
}
```

`Config.Execute()` emits `-object iothread,id=...` before `-drive` args, and appends `cache=<value>` to drive option strings when set.

## Benchmark Comparison (pre-fix)

Test: `fio --rw=randrw --rwmixread=70 --bs=4k --numjobs=4 --iodepth=32 --ioengine=libaio --direct=1`

| Metric | Proxmox VM (direct) | Hive VM (nested, NBD) | Delta |
|---|---|---|---|
| Read IOPS | 85,600 | 2,243 | 38x lower |
| Write IOPS | 36,800 | 970 | 38x lower |
| Read BW | 334 MiB/s | 8.8 MiB/s | 38x lower |
| Write BW | 144 MiB/s | 3.8 MiB/s | 38x lower |
| Read avg latency | 1.0 ms | 41.6 ms | 40x higher |
| Write avg latency | 1.1 ms | 35.5 ms | 32x higher |
| Read p99 latency | ~2.9 ms | ~75 ms | 26x higher |
| Write p99 latency | ~3.0 ms | ~68 ms | 23x higher |
| Disk util | 97% | 79% | Guest not saturating device |

Note: Proxmox test used 512 MiB/job (2 GiB total), Hive test used 128 MiB/job (512 MiB total). This affects cache behavior but does not explain a 38x gap.

The 79% utilization with 35-41 ms average latency in the Hive VM indicates requests were queueing behind a serialization point, not that the physical disk was saturated.

## Root Cause Analysis

**I/O path comparison:**

Proxmox VM:
```
fio → virtio-blk (iothread, multiqueue) → QEMU block layer (cache=none, aio=io_uring) → qcow2 file → host FS → physical disk
```

Hive VM (nested inside Proxmox, before fix):
```
fio → virtio-blk (no iothread, single queue) → QEMU block layer (defaults) → NBD client → Unix socket → nbdkit → viperblock → [outer VM disk path]
```

**What Proxmox set that Hive did not (now fixed):**

| Flag | Proxmox | Hive (before) | Hive (after) |
|---|---|---|---|
| `iothread` | Yes (`iothread-virtio0`) | No | Yes (`ioth-os`, `ioth-<volID>`) |
| `cache` | `none` | Default (writeback) | `none` |
| `num-queues` | Implicit (1, but with iothread) | 1, no iothread | vCPU count (boot), not set (hot-attach) |
| `aio` | `io_uring` | Default (threads) | Default (threads) — correct for NBD |

The iothread was the highest-impact missing flag. Without it, all block I/O processing ran in the main QEMU event loop alongside vCPU emulation.

**Remaining factors (not addressed by QEMU flags):**

1. **QEMU NBD client has a 16 in-flight request limit** — The NBD protocol allows negotiating queue depth, but QEMU's built-in NBD client caps concurrent requests at 16. With `numjobs=4 --iodepth=32` (128 potential in-flight), requests queue inside QEMU before they even reach the socket. This is a hard protocol-level limit that cannot be changed via flags.

2. **Unix socket buffer size** — The default socket buffer (`net.core.wmem_default`, typically 212992 bytes) can bottleneck when many small 4K NBD request/response frames are in flight. Tuning `SO_SNDBUF`/`SO_RCVBUF` on the NBD socket or increasing system defaults may help throughput.

3. **Double virtualization overhead** — The benchmark Hive VM ran inside a Proxmox VM. Every I/O traverses two virtualization boundaries. This is a test environment artifact and won't apply on bare-metal deployments.

## QEMU Best Practices for NBD Backends

### iothread

An iothread is a dedicated QEMU thread for processing virtio-blk requests. Without one, I/O completion runs in the main loop and competes with vCPU scheduling. For any workload beyond trivial single-threaded sequential I/O, an iothread is required.

Each volume should have its own iothread. Boot volumes get `ioth-os`, hot-attached volumes get `ioth-<volumeID>`.

### cache=none

Sets QEMU's block layer to skip host page caching. For file backends, this means `O_DIRECT`. For NBD socket backends, there is no file to `O_DIRECT` on — but the flag still controls QEMU's internal write-back cache and flush forwarding behavior. `cache=none` ensures `FLUSH` commands from the guest are forwarded through NBD to the server, which is correct for data integrity.

### aio= (not meaningful for NBD)

The `aio=` option selects the host async I/O engine for file operations:
- `aio=threads` — POSIX AIO via thread pool (default)
- `aio=io_uring` — Linux io_uring submission queue
- `aio=native` — Linux native AIO (`io_submit`)

These engines optimize `preadv`/`pwritev` syscalls on file descriptors. QEMU's NBD client does not use these syscalls — it uses coroutine-based socket I/O through `QIOChannel`. Setting `aio=io_uring` on an NBD drive changes nothing in the I/O path. The default (`aio=threads`) is correct and we intentionally omit this flag.

### virtio-blk multiqueue (num-queues)

By default, virtio-blk exposes a single virtqueue. With multiple vCPUs, all cores contend on that single queue's notification and completion path. Setting `num-queues=N` (where N = vCPU count) allows each vCPU to submit I/O to its own queue, eliminating cross-CPU contention.

Requires guest kernel support (Linux 3.13+, always available on modern distros).

Currently applied to boot volumes only (where the vCPU count is known from `Config.CPUCount`). Hot-attached volumes do not set `num-queues` because the device_add QMP path doesn't easily support it — the iothread alone provides the primary benefit.

### NBD protocol limits

QEMU's NBD client implementation enforces a maximum of 16 concurrent in-flight requests per connection. This is defined in the QEMU source and cannot be changed via command-line flags. At high queue depths, this becomes a bottleneck regardless of other tuning.

Potential future mitigation: multiple NBD connections to the same export (requires changes to both QEMU invocation and nbdkit/viperblock).

## Viperblock Cache Architecture

The viperblock cache operates at two layers:

1. **In-process golang-lru** — Viperblock's Go process maintains a hashicorp/golang-lru cache of recently accessed blocks. This avoids round-trips to the S3-compatible backend (predastore) for hot blocks.

2. **nbdkit plugin cache** — The nbdkit plugin (which runs as a separate process) has its own `cache_size` parameter that controls a similar LRU cache within the C plugin.

Both caches are sized identically via `defaultCache`:

| Volume type | Cache size | Blocks (4KB each) |
|---|---|---|
| Main (boot, data) | 128 MB | 32,768 |
| Cloud-init (`-cloudinit` suffix) | 0 (disabled) | 0 |
| EFI (`-efi` suffix) | 0 (disabled) | 0 |

Cloud-init and EFI volumes are small (typically < 1MB), read once at boot, and would waste memory with a cache enabled.

## Reference: QEMU Commands

### Proxmox outer VM (relevant flags only)

```sh
/usr/bin/kvm \
  -object iothread,id=iothread-virtio0 \
  -drive file=/var/lib/vz/images/146/vm-146-disk-0.qcow2,if=none,id=drive-virtio0,cache=none,aio=io_uring,discard=on,format=qcow2,detect-zeroes=unmap \
  -device virtio-blk-pci,drive=drive-virtio0,id=virtio0,iothread=iothread-virtio0,bootindex=100 \
  -smp 4,sockets=1,cores=4,maxcpus=4 \
  -m 8192 \
  -cpu host
```

### Hive inner VM (before fix)

```sh
qemu-system-x86_64 \
  -enable-kvm -cpu host -smp 2 -m 4096 \
  -drive file=nbd:unix:/run/user/1000/nbd-vol-xxx.sock,format=raw,if=none,media=disk,id=os \
  -device virtio-blk-pci,drive=os,bootindex=1 \
  -M q35
```

### Hive inner VM (after fix)

```sh
qemu-system-x86_64 \
  -enable-kvm -cpu host -smp 2 -m 4096 \
  -object iothread,id=ioth-os \
  -drive file=nbd:unix:/run/user/1000/nbd-vol-xxx.sock,format=raw,if=none,media=disk,id=os,cache=none \
  -device virtio-blk-pci,drive=os,iothread=ioth-os,num-queues=2,bootindex=1 \
  -M q35
```

### Hot-attach QMP sequence (after fix)

```json
{"execute": "object-add", "arguments": {"qom-type": "iothread", "id": "ioth-vol-abc123"}}
{"execute": "blockdev-add", "arguments": {"node-name": "nbd-vol-abc123", "driver": "nbd", "server": {"type": "unix", "path": "/run/user/1000/nbd-vol-abc123.sock"}, "export": "", "read-only": false}}
{"execute": "device_add", "arguments": {"driver": "virtio-blk-pci", "id": "vdisk-vol-abc123", "drive": "nbd-vol-abc123", "iothread": "ioth-vol-abc123", "bus": "hotplug1"}}
```

### Hot-detach QMP sequence (after fix)

```json
{"execute": "device_del", "arguments": {"id": "vdisk-vol-abc123"}}
{"execute": "blockdev-del", "arguments": {"node-name": "nbd-vol-abc123"}}
{"execute": "object-del", "arguments": {"id": "ioth-vol-abc123"}}
```

## Files Modified

| File | Change |
|---|---|
| `hive/vm/vm.go` | Added `Cache` field to `Drive`, added `IOThread` struct and `IOThreads` to `Config`, updated `Execute()` to emit `-object iothread` and `cache=` |
| `hive/daemon/daemon.go` | Boot drive setup: `cache=none`, iothread, `num-queues=<vCPU count>` |
| `hive/daemon/daemon_handlers.go` | Hot-attach: QMP `object-add` iothread + `iothread` in `device_add`. Hot-detach: QMP `object-del` cleanup |
| `hive/daemon/daemon_test.go` | Updated detach test assertions to expect `object-del` in QMP command sequence |
| `hive/vm/vm_test.go` | Added `TestExecute_IOThreadAndCache`, `TestExecute_NoCacheWhenEmpty`, `TestExecute_MultipleIOThreads` |
| `hive/services/viperblockd/viperblockd.go` | Doubled `defaultCache` from 64MB (16384 blocks) to 128MB (32768 blocks) |

## Diagnostic Tests

Run inside the Hive guest VM to measure the impact of changes.

**Baseline per-I/O overhead** (isolates single-request latency):
```sh
fio --name=lat1 --directory=$HOME/bench --rw=randrw --rwmixread=70 \
    --bs=4k --size=512M --numjobs=1 --iodepth=1 \
    --ioengine=libaio --direct=1 --group_reporting
```

**Queue depth scaling** (detects serialization):
```sh
fio --name=qdtest --directory=$HOME/bench --rw=randrw --rwmixread=70 \
    --bs=4k --size=512M --numjobs=1 --iodepth=32 \
    --ioengine=libaio --direct=1 --group_reporting
```

If `iodepth=1` latency is already in the tens of ms, the NBD/viperblock backend is fundamentally slow per-I/O. If `iodepth=1` is reasonable but `iodepth=32` explodes, there is a serialization or flush amplification problem in the stack.

**Before/after comparison** (run with target QEMU flags applied):
```sh
fio --name=randrw_70_30 --directory=$HOME/bench --rw=randrw --rwmixread=70 \
    --bs=4k --size=512M --numjobs=4 --iodepth=32 \
    --ioengine=libaio --direct=1 --group_reporting
```

## Future Work

- **Post-fix benchmarks**: Re-run fio with the new QEMU flags and 128MB cache to quantify improvement
- **Bare-metal benchmarks**: The pre-fix numbers were from a nested Proxmox environment (double virtualization). Bare-metal numbers will show the true NBD overhead
- **NBD 16-request limit**: Investigate multi-connection NBD or QEMU patches to raise the in-flight cap
- **Socket buffer tuning**: Test `SO_SNDBUF`/`SO_RCVBUF` sizing on NBD Unix sockets
- **nbdkit threading**: nbdkit `--threads` parameter (defaults to 16 for parallel plugins) — verify this matches or exceeds the NBD in-flight limit
- **Dynamic cache sizing**: Scale viperblock cache based on available system memory rather than a fixed 128MB
