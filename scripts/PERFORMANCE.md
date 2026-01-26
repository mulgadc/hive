# Disk performance

### VM

Environment

* Using viperblock (tcp, not socket)
* Predastore (distributed, rs(2,1), 3 local nodes)

## Testing automation

To automate tests and run the suite end-to-end follow the instructions below

### Fresh state

To reset state and delete all existing data run:

```sh
./scripts/reset-dev-env.sh
```

This only needs to be done when you init the environment or major data corruption due to development.

### Launch benchmarking instance

Launch a benchmarking VM, which we can refer back when making changes to our backend for performance tuning. Only needs to be done once.

TODO: Use EC2 tags to mark instance for benchmarking.

```sh
./scripts/run-instance.sh
```

### Run test cases

When code changes are applied benchmark using the process below

Reset services first to apply latest changes:

```sh
./scripts/stop-dev.sh
PPROF_ENABLED=1 PPROF_OUTPUT=/tmp/hive-vm.prof ./scripts/start-dev.sh
```

Once services are started, connect to the benchmarking VM

```sh
./scripts/run-bench.sh
```

This will run the benchmark listed in `./scripts/disk-performance.sh` and store the results in `/tmp/hive-vm-disk.log`

The Linux `perf` tool is also used to benchmark the `nbdkit` process used to create the QEMU > disk > NBD > viperblock > predastore sequence, which is an important tool for debugging purposes. The results will be stored in `/tmp/hive-nbdkit-perf.data`

### Analyze results

Place the output of `/tmp/hive-vm-disk.log` into the results section below, increment the VM-$ID as the test case, and provide a short description of the last changes that were benchmarked.

Store the benchmark file in `predastore-rewrite/tests/hive-vm$id.prof` for later analysis.

# Results

# Host

```sh
randrw_70_30: (g=0): rw=randrw, bs=(R) 4096B-4096B, (W) 4096B-4096B, (T) 4096B-4096B, ioengine=libaio, iodepth=32
...
fio-3.36
Starting 4 processes
Jobs: 4 (f=4)
randrw_70_30: (groupid=0, jobs=4): err= 0: pid=348072: Sun Jan 25 09:53:24 2026
  read: IOPS=73.4k, BW=287MiB/s (301MB/s)(357MiB/1247msec)
    slat (usec): min=2, max=453, avg= 4.70, stdev= 4.20
    clat (usec): min=186, max=8157, avg=1032.68, stdev=400.15
     lat (usec): min=190, max=8160, avg=1037.38, stdev=400.16
    clat percentiles (usec):
     |  1.00th=[  469],  5.00th=[  570], 10.00th=[  644], 20.00th=[  766],
     | 30.00th=[  873], 40.00th=[  963], 50.00th=[ 1037], 60.00th=[ 1090],
     | 70.00th=[ 1156], 80.00th=[ 1237], 90.00th=[ 1336], 95.00th=[ 1450],
     | 99.00th=[ 1827], 99.50th=[ 2573], 99.90th=[ 6783], 99.95th=[ 7701],
     | 99.99th=[ 7898]
   bw (  KiB/s): min=295368, max=300552, per=100.00%, avg=297960.00, stdev=699.39, samples=8
   iops        : min=73842, max=75138, avg=74490.00, stdev=174.85, samples=8
  write: IOPS=31.7k, BW=124MiB/s (130MB/s)(155MiB/1247msec); 0 zone resets
    slat (usec): min=2, max=442, avg= 5.31, stdev= 5.14
    clat (usec): min=430, max=11544, avg=1623.47, stdev=622.56
     lat (usec): min=614, max=11552, avg=1628.79, stdev=622.59
    clat percentiles (usec):
     |  1.00th=[  971],  5.00th=[ 1057], 10.00th=[ 1106], 20.00th=[ 1221],
     | 30.00th=[ 1352], 40.00th=[ 1500], 50.00th=[ 1598], 60.00th=[ 1680],
     | 70.00th=[ 1762], 80.00th=[ 1860], 90.00th=[ 1991], 95.00th=[ 2147],
     | 99.00th=[ 4228], 99.50th=[ 5800], 99.90th=[ 8586], 99.95th=[10421],
     | 99.99th=[11076]
   bw (  KiB/s): min=126792, max=130992, per=100.00%, avg=128892.00, stdev=573.25, samples=8
   iops        : min=31698, max=32748, avg=32223.00, stdev=143.31, samples=8
  lat (usec)   : 250=0.01%, 500=1.36%, 750=11.76%, 1000=18.70%
  lat (msec)   : 2=64.66%, 4=3.00%, 10=0.50%, 20=0.02%
  cpu          : usr=3.63%, sys=18.19%, ctx=54527, majf=0, minf=45
  IO depths    : 1=0.1%, 2=0.1%, 4=0.1%, 8=0.1%, 16=0.1%, 32=99.9%, >=64=0.0%
     submit    : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.0%, 64=0.0%, >=64=0.0%
     complete  : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.1%, 64=0.0%, >=64=0.0%
     issued rwts: total=91488,39584,0,0 short=0,0,0,0 dropped=0,0,0,0
     latency   : target=0, window=0, percentile=100.00%, depth=32

Run status group 0 (all jobs):
   READ: bw=287MiB/s (301MB/s), 287MiB/s-287MiB/s (301MB/s-301MB/s), io=357MiB (375MB), run=1247-1247msec
  WRITE: bw=124MiB/s (130MB/s), 124MiB/s-124MiB/s (130MB/s-130MB/s), io=155MiB (162MB), run=1247-1247msec

Disk stats (read/write):
  nvme0n1: ios=85209/36912, sectors=681672/296992, merge=0/18, ticks=86392/56671, in_queue=143064, util=70.51%
```

## VM-1

Original distributed predastore - prior to any tweaks.

```sh
fio-3.36
Starting 4 processes
randrw_70_30: Laying out IO file (1 file / 128MiB)
randrw_70_30: Laying out IO file (1 file / 128MiB)
randrw_70_30: Laying out IO file (1 file / 128MiB)
randrw_70_30: Laying out IO file (1 file / 128MiB)
Jobs: 1 (f=0): [_(1),f(1),_(2)][100.0%][r=4752KiB/s,w=2022KiB/s][r=1188,w=505 IOPS][eta 00m:00s]
randrw_70_30: (groupid=0, jobs=4): err= 0: pid=1777: Sun Jan 25 00:13:30 2026
  read: IOPS=1143, BW=4573KiB/s (4683kB/s)(357MiB/80026msec)
    slat (usec): min=2, max=6424, avg=32.55, stdev=141.10
    clat (msec): min=5, max=163, avg=81.30, stdev=12.57
     lat (msec): min=5, max=163, avg=81.33, stdev=12.57
    clat percentiles (msec):
     |  1.00th=[   55],  5.00th=[   63], 10.00th=[   66], 20.00th=[   71],
     | 30.00th=[   75], 40.00th=[   79], 50.00th=[   81], 60.00th=[   84],
     | 70.00th=[   88], 80.00th=[   92], 90.00th=[   97], 95.00th=[  103],
     | 99.00th=[  114], 99.50th=[  118], 99.90th=[  133], 99.95th=[  144],
     | 99.99th=[  157]
   bw (  KiB/s): min= 3598, max= 5343, per=100.00%, avg=4574.91, stdev=81.96, samples=636
   iops        : min=  899, max= 1335, avg=1143.21, stdev=20.51, samples=636
  write: IOPS=494, BW=1979KiB/s (2026kB/s)(155MiB/80026msec); 0 zone resets
    slat (usec): min=3, max=95425, avg=49.70, stdev=1010.83
    clat (usec): min=1738, max=155059, avg=70604.80, stdev=11123.35
     lat (usec): min=1767, max=163488, avg=70654.50, stdev=11139.92
    clat percentiles (msec):
     |  1.00th=[   47],  5.00th=[   54], 10.00th=[   57], 20.00th=[   62],
     | 30.00th=[   65], 40.00th=[   68], 50.00th=[   70], 60.00th=[   73],
     | 70.00th=[   77], 80.00th=[   80], 90.00th=[   85], 95.00th=[   89],
     | 99.00th=[   99], 99.50th=[  103], 99.90th=[  114], 99.95th=[  132],
     | 99.99th=[  155]
   bw (  KiB/s): min= 1303, max= 2750, per=100.00%, avg=1980.87, stdev=67.89, samples=636
   iops        : min=  325, max=  687, avg=494.43, stdev=17.00, samples=636
  lat (msec)   : 2=0.01%, 10=0.01%, 20=0.02%, 50=1.04%, 100=93.80%
  lat (msec)   : 250=5.13%
  cpu          : usr=0.79%, sys=2.55%, ctx=94623, majf=0, minf=49
  IO depths    : 1=0.1%, 2=0.1%, 4=0.1%, 8=0.1%, 16=0.1%, 32=99.9%, >=64=0.0%
     submit    : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.0%, 64=0.0%, >=64=0.0%
     complete  : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.1%, 64=0.0%, >=64=0.0%
     issued rwts: total=91488,39584,0,0 short=0,0,0,0 dropped=0,0,0,0
     latency   : target=0, window=0, percentile=100.00%, depth=32

Run status group 0 (all jobs):
   READ: bw=4573KiB/s (4683kB/s), 4573KiB/s-4573KiB/s (4683kB/s-4683kB/s), io=357MiB (375MB), run=80026-80026msec
  WRITE: bw=1979KiB/s (2026kB/s), 1979KiB/s-1979KiB/s (2026kB/s-2026kB/s), io=155MiB (162MB), run=80026-80026msec

Disk stats (read/write):
  vda: ios=91197/39508, sectors=729576/316120, merge=0/7, ticks=7392287/2782326, in_queue=10178891, util=87.53%
```

## VM-2:

Latest changes (with TLS tweaks, still very slow)

```sh
mkdir: cannot create directory ‘/home/ec2-user/bench’: File exists
randrw_70_30: (g=0): rw=randrw, bs=(R) 4096B-4096B, (W) 4096B-4096B, (T) 4096B-4096B, ioengine=libaio, iodepth=32
...
fio-3.36
Starting 4 processes
Jobs: 4 (f=4): [m(4)][98.3%][r=10.1MiB/s,w=4364KiB/s][r=2597,w=1091 IOPS][eta 00m:01s]
randrw_70_30: (groupid=0, jobs=4): err= 0: pid=1271: Sun Jan 25 00:44:38 2026
  read: IOPS=1617, BW=6470KiB/s (6626kB/s)(357MiB/56559msec)
    slat (usec): min=2, max=85271, avg=35.06, stdev=302.85
    clat (msec): min=11, max=192, avg=57.35, stdev=25.45
     lat (msec): min=11, max=192, avg=57.38, stdev=25.46
    clat percentiles (msec):
     |  1.00th=[   26],  5.00th=[   30], 10.00th=[   33], 20.00th=[   37],
     | 30.00th=[   41], 40.00th=[   45], 50.00th=[   50], 60.00th=[   56],
     | 70.00th=[   63], 80.00th=[   72], 90.00th=[  104], 95.00th=[  111],
     | 99.00th=[  122], 99.50th=[  126], 99.90th=[  138], 99.95th=[  146],
     | 99.99th=[  186]
   bw (  KiB/s): min= 2906, max=11264, per=99.55%, avg=6441.30, stdev=625.51, samples=448
   iops        : min=  726, max= 2816, avg=1609.82, stdev=156.37, samples=448
  write: IOPS=699, BW=2799KiB/s (2867kB/s)(155MiB/56559msec); 0 zone resets
    slat (usec): min=3, max=86263, avg=50.47, stdev=855.55
    clat (usec): min=1076, max=174971, avg=50132.33, stdev=21657.67
     lat (usec): min=1081, max=175056, avg=50182.80, stdev=21676.43
    clat percentiles (msec):
     |  1.00th=[   23],  5.00th=[   27], 10.00th=[   29], 20.00th=[   33],
     | 30.00th=[   36], 40.00th=[   40], 50.00th=[   44], 60.00th=[   50],
     | 70.00th=[   55], 80.00th=[   64], 90.00th=[   89], 95.00th=[   96],
     | 99.00th=[  105], 99.50th=[  109], 99.90th=[  122], 99.95th=[  138],
     | 99.99th=[  169]
   bw (  KiB/s): min= 1150, max= 5405, per=99.52%, avg=2786.41, stdev=272.08, samples=448
   iops        : min=  286, max= 1351, avg=695.86, stdev=68.10, samples=448
  lat (msec)   : 2=0.01%, 10=0.01%, 20=0.10%, 50=54.63%, 100=35.80%
  lat (msec)   : 250=9.47%
  cpu          : usr=1.14%, sys=3.40%, ctx=94054, majf=0, minf=54
  IO depths    : 1=0.1%, 2=0.1%, 4=0.1%, 8=0.1%, 16=0.1%, 32=99.9%, >=64=0.0%
     submit    : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.0%, 64=0.0%, >=64=0.0%
     complete  : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.1%, 64=0.0%, >=64=0.0%
     issued rwts: total=91488,39584,0,0 short=0,0,0,0 dropped=0,0,0,0
     latency   : target=0, window=0, percentile=100.00%, depth=32

Run status group 0 (all jobs):
   READ: bw=6470KiB/s (6626kB/s), 6470KiB/s-6470KiB/s (6626kB/s-6626kB/s), io=357MiB (375MB), run=56559-56559msec
  WRITE: bw=2799KiB/s (2867kB/s), 2799KiB/s-2799KiB/s (2867kB/s-2867kB/s), io=155MiB (162MB), run=56559-56559msec

Disk stats (read/write):
  vda: ios=91137/39520, sectors=729096/362609, merge=0/53, ticks=5216036/1979608, in_queue=7202488, util=83.52%
```

## VM-3

```sh
randrw_70_30: (g=0): rw=randrw, bs=(R) 4096B-4096B, (W) 4096B-4096B, (T) 4096B-4096B, ioengine=libaio, iodepth=32
...
fio-3.36
Starting 4 processes
Jobs: 4 (f=4): [m(4)][98.2%][r=8260KiB/s,w=3407KiB/s][r=2065,w=851 IOPS][eta 00m:01s] 
randrw_70_30: (groupid=0, jobs=4): err= 0: pid=1268: Sun Jan 25 01:00:17 2026
  read: IOPS=1698, BW=6792KiB/s (6955kB/s)(357MiB/53877msec)
    slat (usec): min=2, max=90991, avg=39.06, stdev=619.26
    clat (usec): min=148, max=196603, avg=54590.74, stdev=26796.30
     lat (msec): min=3, max=196, avg=54.63, stdev=26.80
    clat percentiles (msec):
     |  1.00th=[   25],  5.00th=[   30], 10.00th=[   33], 20.00th=[   36],
     | 30.00th=[   40], 40.00th=[   42], 50.00th=[   45], 60.00th=[   48],
     | 70.00th=[   53], 80.00th=[   71], 90.00th=[  106], 95.00th=[  113],
     | 99.00th=[  124], 99.50th=[  129], 99.90th=[  142], 99.95th=[  153],
     | 99.99th=[  184]
   bw (  KiB/s): min= 3006, max=11008, per=99.89%, avg=6785.72, stdev=680.77, samples=428
   iops        : min=  751, max= 2752, avg=1695.84, stdev=170.21, samples=428
  write: IOPS=734, BW=2939KiB/s (3009kB/s)(155MiB/53877msec); 0 zone resets
    slat (usec): min=2, max=90090, avg=57.89, stdev=958.53
    clat (usec): min=479, max=178263, avg=47809.29, stdev=22889.80
     lat (usec): min=1377, max=178318, avg=47867.18, stdev=22897.62
    clat percentiles (msec):
     |  1.00th=[   22],  5.00th=[   26], 10.00th=[   28], 20.00th=[   32],
     | 30.00th=[   35], 40.00th=[   37], 50.00th=[   40], 60.00th=[   43],
     | 70.00th=[   48], 80.00th=[   64], 90.00th=[   91], 95.00th=[   97],
     | 99.00th=[  107], 99.50th=[  112], 99.90th=[  125], 99.95th=[  136],
     | 99.99th=[  171]
   bw (  KiB/s): min= 1096, max= 5296, per=99.94%, avg=2937.33, stdev=296.67, samples=428
   iops        : min=  274, max= 1324, avg=733.64, stdev=74.25, samples=428
  lat (usec)   : 250=0.01%, 500=0.01%
  lat (msec)   : 2=0.01%, 4=0.03%, 10=0.08%, 20=0.31%, 50=67.48%
  lat (msec)   : 100=21.60%, 250=10.49%
  cpu          : usr=1.07%, sys=3.36%, ctx=90705, majf=0, minf=55
  IO depths    : 1=0.1%, 2=0.1%, 4=0.1%, 8=0.1%, 16=0.1%, 32=99.9%, >=64=0.0%
     submit    : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.0%, 64=0.0%, >=64=0.0%
     complete  : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.1%, 64=0.0%, >=64=0.0%
     issued rwts: total=91488,39584,0,0 short=0,0,0,0 dropped=0,0,0,0
     latency   : target=0, window=0, percentile=100.00%, depth=32

Run status group 0 (all jobs):
   READ: bw=6792KiB/s (6955kB/s), 6792KiB/s-6792KiB/s (6955kB/s-6955kB/s), io=357MiB (375MB), run=53877-53877msec
  WRITE: bw=2939KiB/s (3009kB/s), 2939KiB/s-2939KiB/s (3009kB/s-3009kB/s), io=155MiB (162MB), run=53877-53877msec

Disk stats (read/write):
  vda: ios=91349/39509, sectors=745528/363497, merge=0/151, ticks=4960404/1883148, in_queue=6850424, util=83.91%
```

## VM-4

```sh
Starting 4 processes
Jobs: 4 (f=4): [m(4)][98.2%][r=7996KiB/s,w=3261KiB/s][r=1999,w=815 IOPS][eta 00m:01s] 
randrw_70_30: (groupid=0, jobs=4): err= 0: pid=1276: Sun Jan 25 01:32:25 2026
  read: IOPS=1641, BW=6566KiB/s (6723kB/s)(357MiB/55737msec)
    slat (usec): min=2, max=58741, avg=34.74, stdev=295.77
    clat (msec): min=5, max=152, avg=56.52, stdev=21.00
     lat (msec): min=8, max=152, avg=56.55, stdev=21.01
    clat percentiles (msec):
     |  1.00th=[   29],  5.00th=[   35], 10.00th=[   37], 20.00th=[   42],
     | 30.00th=[   44], 40.00th=[   47], 50.00th=[   51], 60.00th=[   55],
     | 70.00th=[   61], 80.00th=[   68], 90.00th=[   95], 95.00th=[  107],
     | 99.00th=[  118], 99.50th=[  122], 99.90th=[  133], 99.95th=[  140],
     | 99.99th=[  144]
   bw (  KiB/s): min= 3049, max= 9986, per=100.00%, avg=6566.14, stdev=504.20, samples=444
   iops        : min=  761, max= 2496, avg=1640.79, stdev=126.05, samples=444
  write: IOPS=710, BW=2841KiB/s (2909kB/s)(155MiB/55737msec); 0 zone resets
    slat (usec): min=3, max=57206, avg=41.13, stdev=419.22
    clat (usec): min=1677, max=132642, avg=49446.66, stdev=18019.07
     lat (usec): min=1682, max=132665, avg=49487.79, stdev=18028.04
    clat percentiles (msec):
     |  1.00th=[   26],  5.00th=[   30], 10.00th=[   33], 20.00th=[   36],
     | 30.00th=[   39], 40.00th=[   42], 50.00th=[   45], 60.00th=[   48],
     | 70.00th=[   54], 80.00th=[   60], 90.00th=[   83], 95.00th=[   92],
     | 99.00th=[  102], 99.50th=[  106], 99.90th=[  113], 99.95th=[  118],
     | 99.99th=[  125]
   bw (  KiB/s): min= 1158, max= 4807, per=100.00%, avg=2843.48, stdev=223.17, samples=444
   iops        : min=  288, max= 1201, avg=710.00, stdev=55.86, samples=444
  lat (msec)   : 2=0.01%, 4=0.01%, 10=0.01%, 20=0.05%, 50=54.19%
  lat (msec)   : 100=39.45%, 250=6.29%
  cpu          : usr=1.10%, sys=3.43%, ctx=94132, majf=0, minf=58
  IO depths    : 1=0.1%, 2=0.1%, 4=0.1%, 8=0.1%, 16=0.1%, 32=99.9%, >=64=0.0%
     submit    : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.0%, 64=0.0%, >=64=0.0%
     complete  : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.1%, 64=0.0%, >=64=0.0%
     issued rwts: total=91488,39584,0,0 short=0,0,0,0 dropped=0,0,0,0
     latency   : target=0, window=0, percentile=100.00%, depth=32

Run status group 0 (all jobs):
   READ: bw=6566KiB/s (6723kB/s), 6566KiB/s-6566KiB/s (6723kB/s-6723kB/s), io=357MiB (375MB), run=55737-55737msec
  WRITE: bw=2841KiB/s (2909kB/s), 2841KiB/s-2841KiB/s (2909kB/s-2909kB/s), io=155MiB (162MB), run=55737-55737msec

Disk stats (read/write):
  vda: ios=91357/39650, sectors=730856/359593, merge=0/60, ticks=5146844/1959932, in_queue=7113268, util=83.98%
```

## VM-5

```sh
fio-3.36
Starting 4 processes
Jobs: 4 (f=4): [m(4)][100.0%][r=9236KiB/s,w=3700KiB/s][r=2309,w=925 IOPS][eta 00m:00s]
randrw_70_30: (groupid=0, jobs=4): err= 0: pid=1270: Sun Jan 25 01:42:58 2026
  read: IOPS=1541, BW=6166KiB/s (6314kB/s)(357MiB/59354msec)
    slat (usec): min=2, max=56026, avg=36.43, stdev=333.29
    clat (msec): min=2, max=204, avg=60.26, stdev=25.50
     lat (msec): min=6, max=204, avg=60.29, stdev=25.50
    clat percentiles (msec):
     |  1.00th=[   29],  5.00th=[   34], 10.00th=[   36], 20.00th=[   41],
     | 30.00th=[   44], 40.00th=[   47], 50.00th=[   52], 60.00th=[   58],
     | 70.00th=[   65], 80.00th=[   74], 90.00th=[  107], 95.00th=[  114],
     | 99.00th=[  126], 99.50th=[  130], 99.90th=[  142], 99.95th=[  148],
     | 99.99th=[  174]
   bw (  KiB/s): min= 2966, max=10023, per=99.86%, avg=6157.11, stdev=568.26, samples=472
   iops        : min=  741, max= 2504, avg=1538.69, stdev=142.04, samples=472
  write: IOPS=666, BW=2668KiB/s (2732kB/s)(155MiB/59354msec); 0 zone resets
    slat (usec): min=3, max=92753, avg=48.53, stdev=877.97
    clat (usec): min=1440, max=182236, avg=52479.40, stdev=21766.69
     lat (usec): min=1444, max=182301, avg=52527.93, stdev=21785.49
    clat percentiles (msec):
     |  1.00th=[   26],  5.00th=[   29], 10.00th=[   32], 20.00th=[   35],
     | 30.00th=[   39], 40.00th=[   42], 50.00th=[   46], 60.00th=[   52],
     | 70.00th=[   57], 80.00th=[   66], 90.00th=[   92], 95.00th=[   99],
     | 99.00th=[  109], 99.50th=[  113], 99.90th=[  128], 99.95th=[  142],
     | 99.99th=[  178]
   bw (  KiB/s): min= 1143, max= 4818, per=99.90%, avg=2665.91, stdev=248.70, samples=472
   iops        : min=  285, max= 1204, avg=665.79, stdev=62.20, samples=472
  lat (msec)   : 2=0.01%, 4=0.01%, 10=0.01%, 20=0.05%, 50=50.14%
  lat (msec)   : 100=38.62%, 250=11.16%
  cpu          : usr=1.09%, sys=3.30%, ctx=95179, majf=0, minf=57
  IO depths    : 1=0.1%, 2=0.1%, 4=0.1%, 8=0.1%, 16=0.1%, 32=99.9%, >=64=0.0%
     submit    : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.0%, 64=0.0%, >=64=0.0%
     complete  : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.1%, 64=0.0%, >=64=0.0%
     issued rwts: total=91488,39584,0,0 short=0,0,0,0 dropped=0,0,0,0
     latency   : target=0, window=0, percentile=100.00%, depth=32

Run status group 0 (all jobs):
   READ: bw=6166KiB/s (6314kB/s), 6166KiB/s-6166KiB/s (6314kB/s-6314kB/s), io=357MiB (375MB), run=59354-59354msec
  WRITE: bw=2668KiB/s (2732kB/s), 2668KiB/s-2668KiB/s (2732kB/s-2732kB/s), io=155MiB (162MB), run=59354-59354msec

Disk stats (read/write):
  vda: ios=90996/39461, sectors=727968/362169, merge=0/59, ticks=5473985/2069606, in_queue=7550213, util=84.17%
```

## VM-6

(HTTP2 changes for key-value store, fiber removed completely)

```sh
fio-3.36
Starting 4 processes
Jobs: 4 (f=4): [m(4)][100.0%][r=11.3MiB/s,w=4900KiB/s][r=2893,w=1225 IOPS][eta 00m:00s]
randrw_70_30: (groupid=0, jobs=4): err= 0: pid=1273: Sun Jan 25 03:59:34 2026
  read: IOPS=1759, BW=7038KiB/s (7207kB/s)(357MiB/51993msec)
    slat (usec): min=2, max=51604, avg=32.42, stdev=186.95
    clat (msec): min=11, max=218, avg=52.84, stdev=28.77
     lat (msec): min=11, max=218, avg=52.87, stdev=28.78
    clat percentiles (msec):
     |  1.00th=[   16],  5.00th=[   18], 10.00th=[   22], 20.00th=[   28],
     | 30.00th=[   33], 40.00th=[   39], 50.00th=[   51], 60.00th=[   56],
     | 70.00th=[   61], 80.00th=[   68], 90.00th=[  102], 95.00th=[  110],
     | 99.00th=[  127], 99.50th=[  134], 99.90th=[  148], 99.95th=[  176],
     | 99.99th=[  205]
   bw (  KiB/s): min= 2858, max=23455, per=99.08%, avg=6974.62, stdev=1002.35, samples=412
   iops        : min=  713, max= 5863, avg=1743.23, stdev=250.66, samples=412
  write: IOPS=761, BW=3045KiB/s (3118kB/s)(155MiB/51993msec); 0 zone resets
    slat (usec): min=2, max=104801, avg=50.02, stdev=1047.60
    clat (msec): min=4, max=198, avg=45.84, stdev=24.53
     lat (msec): min=9, max=198, avg=45.89, stdev=24.56
    clat percentiles (msec):
     |  1.00th=[   13],  5.00th=[   16], 10.00th=[   19], 20.00th=[   24],
     | 30.00th=[   29], 40.00th=[   34], 50.00th=[   44], 60.00th=[   48],
     | 70.00th=[   53], 80.00th=[   59], 90.00th=[   87], 95.00th=[   95],
     | 99.00th=[  108], 99.50th=[  114], 99.90th=[  129], 99.95th=[  133],
     | 99.99th=[  186]
   bw (  KiB/s): min= 1014, max=10058, per=99.10%, avg=3018.82, stdev=429.79, samples=412
   iops        : min=  252, max= 2512, avg=753.88, stdev=107.54, samples=412
  lat (msec)   : 10=0.01%, 20=9.15%, 50=44.73%, 100=37.70%, 250=8.42%
  cpu          : usr=1.23%, sys=3.09%, ctx=100768, majf=0, minf=54
  IO depths    : 1=0.1%, 2=0.1%, 4=0.1%, 8=0.1%, 16=0.1%, 32=99.9%, >=64=0.0%
     submit    : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.0%, 64=0.0%, >=64=0.0%
     complete  : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.1%, 64=0.0%, >=64=0.0%
     issued rwts: total=91488,39584,0,0 short=0,0,0,0 dropped=0,0,0,0
     latency   : target=0, window=0, percentile=100.00%, depth=32

Run status group 0 (all jobs):
   READ: bw=7038KiB/s (7207kB/s), 7038KiB/s-7038KiB/s (7207kB/s-7207kB/s), io=357MiB (375MB), run=51993-51993msec
  WRITE: bw=3045KiB/s (3118kB/s), 3045KiB/s-3045KiB/s (3118kB/s-3118kB/s), io=155MiB (162MB), run=51993-51993msec

Disk stats (read/write):
  vda: ios=91080/39481, sectors=728640/358177, merge=0/61, ticks=4812298/1810918, in_queue=6629288, util=85.27%
```

## VM-7

nbdkit blockstore implemented

```sh
Jobs: 4 (f=4): [m(4)][100.0%][r=27.2MiB/s,w=12.2MiB/s][r=6955,w=3111 IOPS][eta 00m:00s]
randrw_70_30: (groupid=0, jobs=4): err= 0: pid=1275: Sun Jan 25 11:22:28 2026
  read: IOPS=6887, BW=26.9MiB/s (28.2MB/s)(357MiB/13283msec)
    slat (usec): min=2, max=11798, avg=13.81, stdev=58.87
    clat (usec): min=1493, max=41386, avg=13540.20, stdev=1882.40
     lat (usec): min=2071, max=41393, avg=13554.01, stdev=1881.48
    clat percentiles (usec):
     |  1.00th=[10814],  5.00th=[11469], 10.00th=[11863], 20.00th=[12387],
     | 30.00th=[12649], 40.00th=[12911], 50.00th=[13304], 60.00th=[13566],
     | 70.00th=[13960], 80.00th=[14484], 90.00th=[15401], 95.00th=[16188],
     | 99.00th=[19006], 99.50th=[23987], 99.90th=[30540], 99.95th=[39060],
     | 99.99th=[40109]
   bw (  KiB/s): min=25664, max=28784, per=100.00%, avg=27602.62, stdev=214.29, samples=104
   iops        : min= 6416, max= 7196, avg=6900.50, stdev=53.57, samples=104
  write: IOPS=2980, BW=11.6MiB/s (12.2MB/s)(155MiB/13283msec); 0 zone resets
    slat (usec): min=2, max=11705, avg=16.04, stdev=83.11
    clat (usec): min=1205, max=38467, avg=11587.91, stdev=1719.09
     lat (usec): min=1217, max=38476, avg=11603.95, stdev=1718.13
    clat percentiles (usec):
     |  1.00th=[ 9110],  5.00th=[ 9765], 10.00th=[10159], 20.00th=[10552],
     | 30.00th=[10814], 40.00th=[11076], 50.00th=[11338], 60.00th=[11600],
     | 70.00th=[11994], 80.00th=[12387], 90.00th=[13173], 95.00th=[14091],
     | 99.00th=[16319], 99.50th=[19006], 99.90th=[36439], 99.95th=[37487],
     | 99.99th=[38011]
   bw (  KiB/s): min=10824, max=13416, per=100.00%, avg=11947.92, stdev=162.76, samples=104
   iops        : min= 2706, max= 3354, avg=2986.77, stdev=40.67, samples=104
  lat (msec)   : 2=0.01%, 4=0.04%, 10=2.46%, 20=96.79%, 50=0.69%
  cpu          : usr=1.74%, sys=5.43%, ctx=105710, majf=0, minf=56
  IO depths    : 1=0.1%, 2=0.1%, 4=0.1%, 8=0.1%, 16=0.1%, 32=99.9%, >=64=0.0%
     submit    : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.0%, 64=0.0%, >=64=0.0%
     complete  : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.1%, 64=0.0%, >=64=0.0%
     issued rwts: total=91488,39584,0,0 short=0,0,0,0 dropped=0,0,0,0
     latency   : target=0, window=0, percentile=100.00%, depth=32

Run status group 0 (all jobs):
   READ: bw=26.9MiB/s (28.2MB/s), 26.9MiB/s-26.9MiB/s (28.2MB/s-28.2MB/s), io=357MiB (375MB), run=13283-13283msec
  WRITE: bw=11.6MiB/s (12.2MB/s), 11.6MiB/s-11.6MiB/s (12.2MB/s-12.2MB/s), io=155MiB (162MB), run=13283-13283msec

Disk stats (read/write):
  vda: ios=90513/39210, sectors=724104/360088, merge=0/53, ticks=1221547/452449, in_queue=1673996, util=78.99%
```

## VM-8

nbdkit using unix sockets

```sh
fio-3.36
Starting 4 processes
Jobs: 4 (f=4): [m(4)][100.0%][r=27.1MiB/s,w=12.1MiB/s][r=6935,w=3109 IOPS][eta 00m:00s]
randrw_70_30: (groupid=0, jobs=4): err= 0: pid=1275: Sun Jan 25 20:59:42 2026
  read: IOPS=6815, BW=26.6MiB/s (27.9MB/s)(357MiB/13424msec)
    slat (usec): min=2, max=2207, avg=14.43, stdev=25.06
    clat (usec): min=2792, max=45592, avg=13693.34, stdev=1905.06
     lat (usec): min=2797, max=45616, avg=13707.77, stdev=1905.01
    clat percentiles (usec):
     |  1.00th=[10814],  5.00th=[11600], 10.00th=[11994], 20.00th=[12518],
     | 30.00th=[12780], 40.00th=[13173], 50.00th=[13435], 60.00th=[13829],
     | 70.00th=[14222], 80.00th=[14615], 90.00th=[15533], 95.00th=[16319],
     | 99.00th=[18744], 99.50th=[23200], 99.90th=[41157], 99.95th=[43254],
     | 99.99th=[44303]
   bw (  KiB/s): min=25459, max=28823, per=99.95%, avg=27248.81, stdev=215.51, samples=104
   iops        : min= 6364, max= 7205, avg=6812.00, stdev=53.90, samples=104
  write: IOPS=2948, BW=11.5MiB/s (12.1MB/s)(155MiB/13424msec); 0 zone resets
    slat (usec): min=2, max=3816, avg=16.57, stdev=31.27
    clat (usec): min=2245, max=43121, avg=11691.49, stdev=1716.26
     lat (usec): min=2282, max=43141, avg=11708.06, stdev=1716.24
    clat percentiles (usec):
     |  1.00th=[ 9110],  5.00th=[ 9896], 10.00th=[10159], 20.00th=[10552],
     | 30.00th=[10945], 40.00th=[11207], 50.00th=[11469], 60.00th=[11731],
     | 70.00th=[12125], 80.00th=[12518], 90.00th=[13304], 95.00th=[14091],
     | 99.00th=[16057], 99.50th=[18220], 99.90th=[26870], 99.95th=[41681],
     | 99.99th=[42730]
   bw (  KiB/s): min=10296, max=13408, per=100.00%, avg=11805.27, stdev=194.58, samples=104
   iops        : min= 2574, max= 3352, avg=2951.15, stdev=48.64, samples=104
  lat (msec)   : 4=0.02%, 10=2.19%, 20=97.15%, 50=0.64%
  cpu          : usr=1.87%, sys=5.56%, ctx=103112, majf=0, minf=60
  IO depths    : 1=0.1%, 2=0.1%, 4=0.1%, 8=0.1%, 16=0.1%, 32=99.9%, >=64=0.0%
     submit    : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.0%, 64=0.0%, >=64=0.0%
     complete  : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.1%, 64=0.0%, >=64=0.0%
     issued rwts: total=91488,39584,0,0 short=0,0,0,0 dropped=0,0,0,0
     latency   : target=0, window=0, percentile=100.00%, depth=32

Run status group 0 (all jobs):
   READ: bw=26.6MiB/s (27.9MB/s), 26.6MiB/s-26.6MiB/s (27.9MB/s-27.9MB/s), io=357MiB (375MB), run=13424-13424msec
  WRITE: bw=11.5MiB/s (12.1MB/s), 11.5MiB/s-11.5MiB/s (12.1MB/s-12.1MB/s), io=155MiB (162MB), run=13424-13424msec

Disk stats (read/write):
  vda: ios=91215/39526, sectors=729720/357992, merge=0/0, ticks=1245726/460340, in_queue=1706067, util=80.32%
```

## VM-9

Less debugging logs

```sh
fio-3.36
Starting 4 processes
Jobs: 4 (f=4): [m(4)][100.0%][r=27.2MiB/s,w=12.2MiB/s][r=6962,w=3127 IOPS][eta 00m:00s]
randrw_70_30: (groupid=0, jobs=4): err= 0: pid=1276: Sun Jan 25 21:42:38 2026
  read: IOPS=6812, BW=26.6MiB/s (27.9MB/s)(357MiB/13429msec)
    slat (usec): min=2, max=3403, avg=14.21, stdev=22.89
    clat (usec): min=3925, max=38495, avg=13696.34, stdev=1752.82
     lat (usec): min=3931, max=38512, avg=13710.56, stdev=1752.83
    clat percentiles (usec):
     |  1.00th=[10945],  5.00th=[11600], 10.00th=[11994], 20.00th=[12518],
     | 30.00th=[12780], 40.00th=[13173], 50.00th=[13435], 60.00th=[13698],
     | 70.00th=[14091], 80.00th=[14615], 90.00th=[15533], 95.00th=[16450],
     | 99.00th=[19792], 99.50th=[23462], 99.90th=[27919], 99.95th=[28967],
     | 99.99th=[30540]
   bw (  KiB/s): min=25848, max=28888, per=100.00%, avg=27286.46, stdev=220.67, samples=104
   iops        : min= 6462, max= 7222, avg=6821.62, stdev=55.17, samples=104
  write: IOPS=2947, BW=11.5MiB/s (12.1MB/s)(155MiB/13429msec); 0 zone resets
    slat (usec): min=2, max=1246, avg=16.45, stdev=22.27
    clat (usec): min=4164, max=28675, avg=11698.89, stdev=1545.51
     lat (usec): min=4169, max=28686, avg=11715.34, stdev=1545.65
    clat percentiles (usec):
     |  1.00th=[ 9241],  5.00th=[ 9896], 10.00th=[10159], 20.00th=[10683],
     | 30.00th=[10945], 40.00th=[11207], 50.00th=[11469], 60.00th=[11731],
     | 70.00th=[12125], 80.00th=[12518], 90.00th=[13304], 95.00th=[14222],
     | 99.00th=[16712], 99.50th=[19006], 99.90th=[26084], 99.95th=[26608],
     | 99.99th=[28443]
   bw (  KiB/s): min=10576, max=13512, per=100.00%, avg=11819.08, stdev=193.66, samples=104
   iops        : min= 2644, max= 3378, avg=2954.77, stdev=48.41, samples=104
  lat (msec)   : 4=0.01%, 10=2.02%, 20=97.22%, 50=0.77%
  cpu          : usr=1.88%, sys=5.57%, ctx=103081, majf=0, minf=56
  IO depths    : 1=0.1%, 2=0.1%, 4=0.1%, 8=0.1%, 16=0.1%, 32=99.9%, >=64=0.0%
     submit    : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.0%, 64=0.0%, >=64=0.0%
     complete  : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.1%, 64=0.0%, >=64=0.0%
     issued rwts: total=91488,39584,0,0 short=0,0,0,0 dropped=0,0,0,0
     latency   : target=0, window=0, percentile=100.00%, depth=32

Run status group 0 (all jobs):
   READ: bw=26.6MiB/s (27.9MB/s), 26.6MiB/s-26.6MiB/s (27.9MB/s-27.9MB/s), io=357MiB (375MB), run=13429-13429msec
  WRITE: bw=11.5MiB/s (12.1MB/s), 11.5MiB/s-11.5MiB/s (12.1MB/s-12.1MB/s), io=155MiB (162MB), run=13429-13429msec

Disk stats (read/write):
  vda: ios=91203/39526, sectors=729624/357984, merge=0/0, ticks=1245361/460522, in_queue=1705884, util=79.61%
```

---

# Findings and Suggestions

## Performance Summary

| Metric | VM (viperblock→predastore) | Host NVMe | Ratio |
|--------|---------------------------|-----------|-------|
| **Read IOPS** | 1,143 | 73,400 | **64x slower** |
| **Write IOPS** | 494 | 31,700 | **64x slower** |
| **Read Bandwidth** | 4.5 MB/s | 287 MB/s | **64x slower** |
| **Write Bandwidth** | 2.0 MB/s | 124 MB/s | **62x slower** |
| **Read Latency** | 81ms | 1ms | **81x slower** |
| **Write Latency** | 70ms | 1.6ms | **44x slower** |
| **Test Duration** | 80 sec | 1.25 sec | **64x slower** |

## Root Cause: TLS Handshakes Per Request

**Profile analysis (`hive-vm.prof`) reveals 84% of CPU spent on TLS handshakes:**

```
84.06%  crypto/tls.(*Conn).HandshakeContext   ← 84% of CPU!
76.51%  crypto/rsa.(*PrivateKey).Sign         ← RSA certificate signing
56.61%  crypto/internal/fips140/bigmod.addMulVVW2048  ← RSA math operations
```

The viperblock S3 client creates new HTTPS connections for almost every block operation instead of reusing connections.

### Why This Happens

In `viperblock/backends/s3/s3.go:57`:

```go
tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
client = &http.Client{Transport: tr}
```

**Problem:** `http.Transport` uses default `MaxIdleConnsPerHost: 2`

With fio test parameters:
- 4 jobs × 32 queue depth = **128 concurrent requests**
- Only **2 idle connections** reused per host
- **126 requests force new TLS handshakes per batch**

### The Math

- 131,072 total I/O operations (reads + writes)
- Each operation = 1 S3 request
- With only 2 idle connections: ~130,000 TLS handshakes
- Each TLS handshake ≈ 0.5-1ms (RSA operations)
- **Total TLS overhead: ~65-130 seconds** (matches 80s test duration)

## Suggested Fix

### Priority 1: HTTP Connection Pool (viperblock)

**File:** `viperblock/backends/s3/s3.go`

```go
// CURRENT (broken - uses default MaxIdleConnsPerHost: 2):
tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}

// FIX - increase connection pool size:
tr := &http.Transport{
    TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
    MaxIdleConnsPerHost: 100,  // Allow connection reuse!
    MaxIdleConns:        100,
    IdleConnTimeout:     90 * time.Second,
}
```

**Expected improvement:** 50-100x speedup (TLS overhead: 80s → <0.1s)

### Priority 2: Consider TLS Session Resumption (predastore)

If connections still churn, enable TLS session tickets on the server side to reduce handshake cost from full RSA operations to symmetric key operations.

### Priority 3: Request Batching (future optimization)

For 4KB block operations, consider:
- Coalescing adjacent blocks into single requests
- Using multipart uploads for write batches
- Read-ahead caching for sequential patterns

## Profile Breakdown

| Function | CPU Time | % Total | Category |
|----------|----------|---------|----------|
| `addMulVVW2048` | 582s | 56.6% | RSA 2048-bit math |
| `montgomeryMul` | 733s | 71.2% | Modular multiplication |
| `handshakeContext` | 845s | 82.1% | TLS handshake total |
| `serverHandshake` | 819s | 79.6% | Server-side (predastore) |
| `rsa.decrypt` | 787s | 76.5% | Certificate verification |
| `handleGET` | 60s | 5.8% | Actual S3 GET handling |
| `syscall` | 80s | 7.8% | I/O operations |

**Key insight:** Only ~6% of CPU is spent on actual S3 operations. 84% is wasted on redundant TLS handshakes.

## Validation

After applying the fix, re-run the fio test and profile. Expected results:
- TLS functions should drop from 84% to <5% of CPU
- IOPS should increase 50-100x
- Latency should drop from 70-80ms to 1-5ms

---

# Scaling Considerations

## Connection Scaling Analysis

### Architecture Overview

```
┌─────────────┐     N conns        ┌─────────────┐
│  viperblock │ ─────────────────► │  predastore │
│   (per VM)  │  MaxIdleConns=N    │  (cluster)  │
└─────────────┘                    └─────────────┘
```

Each viperblock instance maintains its own HTTP connection pool to predastore. With multiple VMs, connections multiply.

### Connection Math by Scale

| Scale | VMs | Conns/VM | Total Connections | Memory (est.) | Status |
|-------|-----|----------|-------------------|---------------|--------|
| Dev | 1 | 100 | 100 | ~50 MB | ✅ Safe |
| Small | 8 | 100 | 800 | ~400 MB | ✅ Safe |
| Medium | 32 | 100 | 3,200 | ~1.6 GB | ⚠️ Monitor |
| Large | 100 | 100 | 10,000 | ~5 GB | ⚠️ Tune required |
| XL | 256 | 100 | 25,600 | ~13 GB | 🔴 Must optimize |

**Memory estimate:** ~500KB per TLS connection (buffers, TLS state, goroutine stack)

## Breaking Points

### 1. File Descriptor Limits (First to Hit)

Default Linux limits are often too low:

```bash
# Check current limits
ulimit -n        # Per-process limit (often 1024)
cat /proc/sys/fs/file-max  # System-wide limit
```

**Breaking point:**
- Default `ulimit -n 1024`: ~1,000 connections max
- Default `ulimit -n 65535`: ~65,000 connections max

### 2. Socket/Network Limits

```bash
# Check current settings
sysctl net.core.somaxconn           # Listen backlog (default: 4096)
sysctl net.ipv4.tcp_max_syn_backlog # SYN queue (default: 1024)
sysctl net.core.netdev_max_backlog  # NIC queue (default: 1000)
```

### 3. Memory Pressure

Per-connection overhead:
- Goroutine stack: ~8KB minimum, grows to ~1MB max
- TLS buffers: ~32KB per connection
- TCP buffers: ~128KB per connection (tunable)

**At 10,000 connections:** ~500MB - 1GB overhead

### 4. Ephemeral Port Exhaustion (Client-Side)

```bash
# Check port range
sysctl net.ipv4.ip_local_port_range  # Default: 32768-60999 (~28K ports)
```

With many short-lived connections, ports stuck in TIME_WAIT can exhaust the range.

## Host Configuration (sysctl)

### Recommended Settings for Hive Host

Create `/etc/sysctl.d/99-hive.conf`:

```bash
# =============================================================================
# Hive Platform - System Tuning for High-Connection Workloads
# =============================================================================

# -----------------------------------------------------------------------------
# File Descriptor Limits
# -----------------------------------------------------------------------------
# Increase system-wide file descriptor limit
fs.file-max = 2097152

# Increase inotify limits for file watching
fs.inotify.max_user_watches = 524288
fs.inotify.max_user_instances = 512

# -----------------------------------------------------------------------------
# Network Core Settings
# -----------------------------------------------------------------------------
# Increase socket listen backlog (for predastore accepting connections)
net.core.somaxconn = 65535

# Increase network device backlog (packets queued when NIC faster than CPU)
net.core.netdev_max_backlog = 65535

# Increase maximum receive/send buffer sizes
net.core.rmem_max = 16777216
net.core.wmem_max = 16777216
net.core.rmem_default = 1048576
net.core.wmem_default = 1048576

# Increase option memory buffers
net.core.optmem_max = 65535

# -----------------------------------------------------------------------------
# TCP Settings
# -----------------------------------------------------------------------------
# Increase TCP buffer sizes (min, default, max)
net.ipv4.tcp_rmem = 4096 1048576 16777216
net.ipv4.tcp_wmem = 4096 1048576 16777216

# Increase SYN backlog for high connection rates
net.ipv4.tcp_max_syn_backlog = 65535

# Increase TIME_WAIT bucket count (for connection churn)
net.ipv4.tcp_max_tw_buckets = 2000000

# Enable TCP Fast Open (reduces latency for repeated connections)
net.ipv4.tcp_fastopen = 3

# Reuse TIME_WAIT sockets for new connections (safe for same tuple)
net.ipv4.tcp_tw_reuse = 1

# Increase local port range for outbound connections
net.ipv4.ip_local_port_range = 1024 65535

# Increase max orphaned sockets (connections without process)
net.ipv4.tcp_max_orphans = 262144

# Reduce FIN timeout (faster connection cleanup)
net.ipv4.tcp_fin_timeout = 15

# Enable TCP keepalive with shorter intervals
net.ipv4.tcp_keepalive_time = 60
net.ipv4.tcp_keepalive_intvl = 10
net.ipv4.tcp_keepalive_probes = 6

# -----------------------------------------------------------------------------
# UDP Settings (for QUIC)
# -----------------------------------------------------------------------------
# Increase UDP buffer sizes for QUIC protocol
net.core.rmem_max = 2500000
net.core.wmem_max = 2500000

# -----------------------------------------------------------------------------
# Memory Settings
# -----------------------------------------------------------------------------
# Allow more memory for network buffers under pressure
net.ipv4.tcp_mem = 786432 1048576 1572864

# Virtual memory tuning
vm.swappiness = 10
vm.dirty_ratio = 20
vm.dirty_background_ratio = 5
```

Apply settings:
```bash
sudo sysctl -p /etc/sysctl.d/99-hive.conf
```

### Process Limits (/etc/security/limits.conf)

```bash
# Add to /etc/security/limits.conf or /etc/security/limits.d/hive.conf

# For predastore/hive user
hive    soft    nofile    1048576
hive    hard    nofile    1048576
hive    soft    nproc     unlimited
hive    hard    nproc     unlimited
hive    soft    memlock   unlimited
hive    hard    memlock   unlimited

# Or for all users (dev environments)
*       soft    nofile    1048576
*       hard    nofile    1048576
```

### Systemd Service Limits

If running as systemd service, add to unit file:

```ini
[Service]
LimitNOFILE=1048576
LimitNPROC=infinity
LimitMEMLOCK=infinity
```

## Recommended Configuration by Scale

### Small Deployment (1-16 VMs)

**Host Requirements:**
- CPU: 4+ cores
- RAM: 16+ GB
- Network: 1 Gbps

**Viperblock Settings:**
```go
MaxIdleConnsPerHost: 64
MaxConnsPerHost:     100
IdleConnTimeout:     90 * time.Second
```

**sysctl:** Default settings usually sufficient, but apply limits.conf changes.

### Medium Deployment (17-64 VMs)

**Host Requirements:**
- CPU: 8+ cores
- RAM: 32+ GB
- Network: 10 Gbps

**Viperblock Settings:**
```go
MaxIdleConnsPerHost: 32
MaxConnsPerHost:     64
IdleConnTimeout:     60 * time.Second
```

**sysctl:** Apply full 99-hive.conf settings.

### Large Deployment (65-256 VMs)

**Host Requirements:**
- CPU: 16+ cores
- RAM: 64+ GB
- Network: 10+ Gbps (consider bonding)

**Viperblock Settings:**
```go
MaxIdleConnsPerHost: 16
MaxConnsPerHost:     32
IdleConnTimeout:     30 * time.Second
```

**Additional Considerations:**
- Enable HTTP/2 for connection multiplexing
- Consider multiple predastore instances behind load balancer
- Monitor connection counts and memory usage

### Extra-Large Deployment (256+ VMs)

**Architecture Changes Required:**
- Horizontal scaling of predastore (multiple instances)
- Regional/sharded predastore deployment
- HTTP/2 or QUIC for multiplexing
- Connection pooling gateway (optional)

**Viperblock Settings:**
```go
MaxIdleConnsPerHost: 8
MaxConnsPerHost:     16
IdleConnTimeout:     30 * time.Second
```

## Future Optimizations

### HTTP/2 Multiplexing (High Impact)

Switch predastore to HTTP/2 to multiplex requests over fewer connections:

```
Before (HTTP/1.1):  32 VMs × 100 conns = 3,200 TCP connections
After (HTTP/2):     32 VMs × 2 conns   = 64 TCP connections
                    (each handling 100+ concurrent streams)
```

### Connection Pooling Gateway

For very large deployments, consider a connection pooling layer:

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│ viperblock  │────►│   pooler    │────►│  predastore │
│ (many VMs)  │     │  (few conns)│     │             │
└─────────────┘     └─────────────┘     └─────────────┘
```

### TLS Session Resumption

Enable TLS session tickets to reduce handshake cost for new connections from same clients. Reduces RSA operations from full handshake to symmetric key operations.

## Monitoring Checklist

### Predastore (Server-Side)

```bash
# Active connections
ss -s | grep estab
netstat -an | grep ESTABLISHED | wc -l

# File descriptors
ls /proc/$(pgrep predastore)/fd | wc -l

# Memory usage
ps aux | grep predastore

# Goroutine count (if pprof enabled)
curl http://localhost:6060/debug/pprof/goroutine?debug=1 | head -1
```

### Viperblock (Client-Side)

```bash
# Outbound connections
ss -tn | grep <predastore-ip> | wc -l

# TIME_WAIT sockets (connection churn indicator)
ss -s | grep TIME-WAIT

# Port usage
ss -tn | awk '{print $4}' | cut -d: -f2 | sort -n | uniq -c | sort -rn | head
```

### Key Metrics to Alert On

| Metric | Warning | Critical |
|--------|---------|----------|
| Open file descriptors | >80% of limit | >95% of limit |
| Established connections | >50% of expected | >90% of expected |
| Memory usage | >70% | >90% |
| TIME_WAIT sockets | >10,000 | >50,000 |
| Connection errors | >1% | >5% |
| TLS handshake rate | >100/s sustained | >1000/s sustained |

## Progress

### 2026-01-25: Connection Pool Fix Implemented

**File:** `viperblock/viperblock/backends/s3/s3.go`

**Change:** Updated HTTP Transport configuration with proper connection pooling:

```go
// BEFORE (default MaxIdleConnsPerHost: 2)
tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
client = &http.Client{Transport: tr}

// AFTER (connection pooling enabled)
tr := &http.Transport{
    TLSClientConfig: &tls.Config{InsecureSkipVerify: true},

    // Connection pool settings - critical for performance
    MaxIdleConns:        100,              // Total idle connections across all hosts
    MaxIdleConnsPerHost: 100,              // Idle connections per host (default: 2)
    MaxConnsPerHost:     100,              // Max total connections per host
    IdleConnTimeout:     90 * time.Second, // How long idle connections stay in pool

    // Timeouts
    TLSHandshakeTimeout:   10 * time.Second,
    ResponseHeaderTimeout: 30 * time.Second,
    ExpectContinueTimeout: 1 * time.Second,
}

client := &http.Client{
    Transport: tr,
    Timeout:   60 * time.Second, // Overall request timeout
}
```

**Status:** ✅ Implemented, builds pass

**Result (VM-2):** ~40% improvement, but TLS still at 56% CPU (down from 84%)

---

### 2026-01-25: Server + Client Keep-Alive Optimization

**Problem:** VM-2 showed 40% improvement but TLS handshakes still at 56% CPU. Connections closing too quickly.

**Changes Made:**

**1. Predastore Fiber Config** (`predastore-rewrite/s3/routes.go`):
```go
app := fiber.New(fiber.Config{
    // ... existing settings ...

    // NEW: Keep connections alive longer
    IdleTimeout:  120 * time.Second, // Keep idle connections for 2 min
    ReadTimeout:  60 * time.Second,
    WriteTimeout: 60 * time.Second,
    DisableDefaultDate: true,        // Reduce overhead
})
```

**2. Viperblock Client** (`viperblock/viperblock/backends/s3/s3.go`):
```go
tr := &http.Transport{
    TLSClientConfig: &tls.Config{
        InsecureSkipVerify: true,
        // NEW: TLS session resumption for faster reconnects
        ClientSessionCache: tls.NewLRUClientSessionCache(256),
    },

    // INCREASED: Pool size to exceed concurrency (4×32=128)
    MaxIdleConns:        200,
    MaxIdleConnsPerHost: 200,
    MaxConnsPerHost:     0,                 // Unlimited
    IdleConnTimeout:     120 * time.Second, // Match server

    // NEW: Enable HTTP/2 multiplexing
    ForceAttemptHTTP2: true,
    DisableKeepAlives: false,

    // Timeouts
    ResponseHeaderTimeout: 60 * time.Second,
}

client := &http.Client{
    Transport: tr,
    Timeout:   120 * time.Second, // Longer for large objects
}
```

**Status:** ✅ Implemented, builds pass

**Expected Result:**
- TLS session cache reduces reconnect cost
- HTTP/2 multiplexes requests over fewer connections
- Matched idle timeouts (120s) prevent premature connection closure
- Pool size (200) exceeds concurrency (128)

## TODO

- [x] Implement viperblock connection pool fix (MaxIdleConnsPerHost: 100)
- [x] Increase pool size to exceed concurrency (200)
- [x] Add TLS session resumption cache
- [x] Enable HTTP/2 attempt
- [x] Add server-side idle timeout (Fiber)
- [x] Migrate predastore to net/http with native HTTP/2 support
- [ ] Validate fix with fio benchmark (VM-4)
- [ ] Add predastore connection metrics endpoint
- [ ] Test with 8-VM deployment
- [ ] Create hive-tuning.sh script for automated sysctl configuration
- [ ] Add Prometheus metrics for connection tracking

---

## VM-4: HTTP/2 Implementation (net/http + chi router)

**Date:** 2026-01-25

### Problem: Fasthttp Doesn't Support HTTP/2

The previous attempts (VM-2, VM-3) used Fiber (built on fasthttp) which does not support HTTP/2.
Even with `ForceAttemptHTTP2: true` on the client, the server was responding with HTTP/1.1.

HTTP/1.1 limitations:
- One request per connection (no multiplexing)
- Each of 128 concurrent requests needs its own connection
- Under load, connections churn faster than keep-alive can maintain
- Result: Continuous TLS handshakes consuming 56-84% CPU

### Solution: Native HTTP/2 with net/http

Migrated predastore from Fiber/fasthttp to Go's standard library `net/http` with `chi` router:

**New files:**
- `predastore/s3/httpserver.go` - HTTP/2 server using net/http + chi

**Key changes:**

```go
// TLS config with HTTP/2 ALPN negotiation
tlsConfig := &tls.Config{
    NextProtos: []string{"h2", "http/1.1"},  // HTTP/2 first, fallback to 1.1
    // ...
}

// Server with proper timeouts
server := &http.Server{
    Handler:     chiRouter,
    TLSConfig:   tlsConfig,
    IdleTimeout: 120 * time.Second,
    // ...
}
```

**HTTP/2 is now the default:**
- `predastore.Config.HTTP2Enabled` defaults to `true`
- Can fall back to Fiber with `HTTP2Enabled: false` if needed

### Expected Impact

With HTTP/2 multiplexing:
- 128 concurrent requests can share ~2-4 connections
- TLS handshake overhead eliminated (happens once per connection)
- Single connection can handle thousands of requests
- Expected: >10x improvement over HTTP/1.1 performance

### Next Steps

1. Rebuild and restart services with HTTP/2 enabled
2. Run fio benchmark (VM-4) to measure improvement
3. Compare CPU profile - TLS handshakes should drop to <5%
4. If successful, consider removing Fiber dependency entirely

---

## VM-6: Critical HTTP/2 Client Fix (http2.ConfigureTransport)

**Date:** 2026-01-25

### Problem: HTTP/2 Not Actually Working

After migrating predastore to net/http with HTTP/2 support, profiling still showed **60% CPU on TLS handshakes**:

```
60.56%  crypto/tls.(*Conn).HandshakeContext
```

Additionally, logs showed **126K+ HTTP/1.1 requests** despite HTTP/2 being configured:

```bash
cat ~/hive/logs/predastore.log | grep "HTTP/1" | wc -l
# 126277
```

### Root Cause: Custom TLSClientConfig Breaks HTTP/2

When using `http.Transport` with a custom `TLSClientConfig`, Go's default HTTP/2 support is **silently disabled**. The `ForceAttemptHTTP2: true` flag alone is **NOT sufficient**.

**The problem code (in multiple S3 clients):**

```go
// THIS DOES NOT ENABLE HTTP/2!
tr := &http.Transport{
    TLSClientConfig: &tls.Config{
        InsecureSkipVerify: true,
    },
    ForceAttemptHTTP2: true,  // ← Ignored when custom TLSClientConfig is set
}
```

### Solution: http2.ConfigureTransport()

The fix requires explicitly calling `http2.ConfigureTransport()` from `golang.org/x/net/http2`:

```go
import "golang.org/x/net/http2"

tr := &http.Transport{
    TLSClientConfig: &tls.Config{
        InsecureSkipVerify: true,
        // Ensure HTTP/2 ALPN is advertised
        NextProtos: []string{"h2", "http/1.1"},
    },
    ForceAttemptHTTP2: true,
}

// CRITICAL: This is REQUIRED when using custom TLSClientConfig
if err := http2.ConfigureTransport(tr); err != nil {
    slog.Warn("Failed to configure HTTP/2", "error", err)
}
```

### Files Updated

All S3 clients across the codebase were updated:

| File | Purpose |
|------|---------|
| `viperblock/viperblock/backends/s3/s3.go` | Block storage S3 backend |
| `hive/s3client/s3client.go` | Hive S3 client |
| `hive/handlers/ec2/key/service_impl.go` | EC2 key pair operations |
| `hive/handlers/ec2/volume/service_impl.go` | EBS volume operations |
| `hive/handlers/ec2/image/service_impl.go` | AMI image operations |
| `hive/utils/utils.go` | Shared S3 client utility |
| `hive/cmd/hive/cmd/admin.go` | Admin CLI operations |
| `predastore/s3db/client.go` | S3DB distributed client |

### Complete HTTP/2 Client Pattern

```go
import (
    "crypto/tls"
    "net/http"
    "time"

    "golang.org/x/net/http2"
)

func createHTTP2Client() *http.Client {
    tr := &http.Transport{
        TLSClientConfig: &tls.Config{
            InsecureSkipVerify: true,
            // TLS session resumption for faster reconnects
            ClientSessionCache: tls.NewLRUClientSessionCache(256),
            // Ensure HTTP/2 ALPN is advertised
            NextProtos: []string{"h2", "http/1.1"},
        },

        // Connection pool settings
        MaxIdleConns:        200,
        MaxIdleConnsPerHost: 200,
        MaxConnsPerHost:     0,                 // Unlimited
        IdleConnTimeout:     120 * time.Second,

        // Keep-alive settings
        DisableKeepAlives: false,
        ForceAttemptHTTP2: true,

        // Timeouts
        TLSHandshakeTimeout:   10 * time.Second,
        ResponseHeaderTimeout: 60 * time.Second,
        ExpectContinueTimeout: 1 * time.Second,
    }

    // CRITICAL: Configure HTTP/2 support with custom TLS config
    // Without this, the transport falls back to HTTP/1.1!
    if err := http2.ConfigureTransport(tr); err != nil {
        // Log but continue - will fall back to HTTP/1.1
        slog.Warn("Failed to configure HTTP/2", "error", err)
    }

    return &http.Client{
        Transport: tr,
        Timeout:   120 * time.Second,
    }
}
```

### Why This Happens

Go's `net/http` package has automatic HTTP/2 support, but only when:

1. **Default transport is used**, OR
2. **No custom TLSClientConfig is set**

When you provide a custom `TLSClientConfig` (even for something as simple as `InsecureSkipVerify: true`), Go assumes you want full control and disables automatic HTTP/2 configuration.

The `http2.ConfigureTransport()` function:
- Wraps the transport with HTTP/2 support
- Sets up the HTTP/2 frame handler
- Enables connection multiplexing
- Handles ALPN negotiation properly

### Expected Impact

With proper HTTP/2 multiplexing:
- 128 concurrent requests share 2-4 connections (instead of 128 connections)
- TLS handshakes drop from 126K to ~10-20 for the entire test
- CPU overhead from TLS should drop from 60% to <5%
- Expected IOPS improvement: 10-50x

### Verification

After deploying the fix, verify HTTP/2 is working:

```bash
# Check predastore logs for HTTP/2 requests
grep "HTTP/2" ~/hive/logs/predastore.log | head

# Count HTTP/1.1 vs HTTP/2 requests
echo "HTTP/1.1: $(grep -c 'HTTP/1' ~/hive/logs/predastore.log)"
echo "HTTP/2.0: $(grep -c 'HTTP/2' ~/hive/logs/predastore.log)"

# Profile should show minimal TLS overhead
go tool pprof -top hive-vm.prof | head -20
# TLS functions should be <5% instead of 60-84%
```

### Lessons Learned

1. **ForceAttemptHTTP2 is misleading** - It doesn't force HTTP/2 when custom TLS is used
2. **Always use http2.ConfigureTransport()** with custom TLSClientConfig
3. **Add NextProtos for ALPN** - Ensures HTTP/2 is advertised during TLS negotiation
4. **Check logs for protocol version** - Don't assume HTTP/2 is working without verification

### TODO

- [ ] Re-run fio benchmark (VM-6) with HTTP/2 client fix
- [ ] Verify TLS overhead drops to <5% in profile
- [ ] Measure actual IOPS improvement
- [ ] Consider request batching for further optimization (126K requests still high even with HTTP/2)

---

## Development Workflow: Cross-Repository Testing

### Mandatory Testing Before Changes

The Hive platform consists of three interdependent repositories:

```
hive ──depends on──► viperblock ──depends on──► predastore
```

**CRITICAL: All tests must pass across all three repositories before committing changes.**

Changes in predastore can break viperblock and hive. Changes in viperblock can break hive.

### Testing Workflow

```bash
# 1. Always test predastore first (base dependency)
cd /path/to/predastore-rewrite
LOG_IGNORE=1 go test ./... -count=1

# 2. Then test viperblock (depends on predastore)
cd /path/to/viperblock
LOG_IGNORE=1 go test ./... -count=1

# 3. Finally test hive (depends on both)
cd /path/to/hive
LOG_IGNORE=1 go test ./... -count=1
```

### go.work Configuration

Each repository uses `go.work` for local development with replace directives:

**hive/go.work:**
```go
go 1.25.5

use .

replace github.com/mulgadc/viperblock => ../viperblock
replace github.com/mulgadc/predastore => ../predastore-rewrite/
```

**viperblock/go.work:**
```go
go 1.25.5

use .

replace github.com/mulgadc/predastore => ../predastore-rewrite/
```

### Common Test Failures and Fixes

| Error | Cause | Fix |
|-------|-------|-----|
| `http: Server closed` | Normal shutdown error treated as failure | Filter out `http.ErrServerClosed` |
| `slice bounds out of range` | Empty/nil data from failed operation | Add bounds validation before slicing |
| Port binding errors | Tests reusing ports too quickly | Use `FindFreePort()` or add retry logic |
| `context deadline exceeded` | Server not ready in time | Increase `waitForServer` timeout |
| API signature mismatch | Dependency updated | Update calling code to match new API |

### Test Environment Variables

```bash
LOG_IGNORE=1  # Suppress log output during tests
```

### Pre-Commit Checklist

- [ ] `cd predastore && go test ./...` passes
- [ ] `cd viperblock && go test ./...` passes
- [ ] `cd hive && go test ./...` passes
- [ ] `go build ./...` succeeds in all repos
- [ ] No new compiler warnings

---

# Viperblock

## VM-7: Viperblock NBD Plugin Profiling (2026-01-25)

### Context

After fixing the TLS/HTTP2 overhead in predastore (VM-6), profiling shifted to the client-side: **nbdkit with the viperblock Go plugin was consuming 100% CPU** during benchmarks. The predastore server was now idle, confirming the bottleneck moved to the viperblock plugin.

### Profiling Approach

**Challenge:** Go's built-in `pprof` CPU profiler uses SIGPROF signals, which **do not work correctly** when Go code runs as a CGO shared library loaded by a C program (nbdkit). The Go runtime doesn't control signal handling in this context.

**Solution:** Use Linux `perf` for system-level profiling:
```bash
# Attach to running nbdkit process
perf record -g -p $(pgrep -f nbdkit) -o /tmp/nbdkit-perf.data -- sleep 60

# Analyze results
perf report -i /tmp/nbdkit-perf.data --stdio --no-children
```

### Profile Results (nbdkit-perf2.data)

| Function | CPU % | Category | Issue |
|----------|-------|----------|-------|
| `runtime.mapassign_fast64` | **27.45%** | Map operations | Map writes during READ path |
| `runtime.memclrNoHeapPointers` | 8.69% | Memory | Map rehashing/growth |
| `viperblock.(*VB).read` | 7.65% | Application | Actual read logic |
| `maps.(*table).rehash` | ~6.4% | Map operations | Constant map resizing |
| `runtime.memhash64` | ~4.1% | Map operations | Hash computation |
| `runtime.mapaccess2_fast64` | ~3.2% | Map operations | Map lookups |

**Total map-related overhead: ~50% of CPU time**

### Root Cause Analysis

The `read()` function in `viperblock/viperblock.go` (lines 1688-1856) creates and populates **new maps on every single read operation**:

```go
// Called on EVERY read - even single block reads
func (vb *VB) read(block uint64, blockLen uint64) (data []byte, err error) {

    // Problem 1: New map created without size hint
    latestWrites := make(BlocksMap)  // Starts with 8 buckets
    for _, wr := range writesCopy {
        latestWrites[wr.Block] = wr  // Rehashes at 6.5, 13, 26, 52... items
    }

    // Problem 2: Another map, same issue
    latestPendingWrites := make(BlocksMap)
    for _, wr := range pendingWritesCopy {
        latestPendingWrites[wr.Block] = wr
    }

    // Problem 3: Yet another map per read
    consecutiveBlocksRead := make(map[uint64]bool)
    // ...
}
```

**Why this is expensive:**
1. **Map allocation**: Go maps start small (8 buckets) and grow by rehashing
2. **Rehashing**: When load factor exceeds 6.5, entire map is rebuilt with 2x buckets
3. **Per-read cost**: With 131K IOPS benchmark, that's 131K × 3 map creations
4. **O(n) preprocessing**: Every read iterates ALL pending writes, even for single-block reads

### Quick Fix Applied

Pre-allocate maps with known capacity to eliminate rehashing:

```go
// Before: Zero capacity, constant rehashing
latestWrites := make(BlocksMap)

// After: Pre-allocated, no rehashing
latestWrites := make(BlocksMap, writesLen)
latestPendingWrites := make(BlocksMap, pendingLen)
consecutiveBlocksRead := make(map[uint64]bool, len(consecutiveBlocks))
```

**Files changed:** `viperblock/viperblock/viperblock.go`

### Architectural Issues & Optimization Ideas

The quick fix reduces rehashing but doesn't address the fundamental design issue: **rebuilding lookup structures on every read**.

#### Current Architecture (Read Path)

```
Read Request
    │
    ├─► Copy all Writes.Blocks (slice copy)
    ├─► Build latestWrites map (O(n) where n = pending writes)
    ├─► Copy all PendingBackendWrites.Blocks (slice copy)
    ├─► Build latestPendingWrites map (O(n))
    ├─► For each requested block:
    │       ├─► Check latestWrites map
    │       ├─► Check latestPendingWrites map
    │       ├─► Check LRU cache
    │       └─► Lookup BlocksToObject map
    └─► Fetch from backend if needed
```

#### Proposed Architecture Options

**Option A: Persistent Write Index**

Maintain a persistent map that's updated incrementally during writes, not rebuilt during reads.

```go
type VB struct {
    // Existing
    Writes Blocks

    // New: Persistent index updated on write, read-only during reads
    WriteIndex sync.Map  // or sharded map with fine-grained locking
}

func (vb *VB) Write(...) {
    // Update WriteIndex atomically during write
    vb.WriteIndex.Store(block, blockData)
}

func (vb *VB) read(...) {
    // O(1) lookup instead of O(n) rebuild
    if data, ok := vb.WriteIndex.Load(block); ok {
        return data
    }
}
```

**Pros:** O(1) read lookup, no per-read allocation
**Cons:** sync.Map has overhead, need careful handling of flush/cleanup

**Option B: Generation-Based Snapshots**

Keep immutable snapshots of write maps, only rebuild when writes occur.

```go
type VB struct {
    writeSnapshot atomic.Pointer[BlocksMap]  // Immutable snapshot
    writeGen      atomic.Uint64              // Generation counter
    lastReadGen   uint64                     // Last seen generation
}

func (vb *VB) Write(...) {
    // Increment generation, snapshot rebuilt lazily
    vb.writeGen.Add(1)
}

func (vb *VB) read(...) {
    currentGen := vb.writeGen.Load()
    if currentGen != vb.lastReadGen {
        // Rebuild snapshot only when writes occurred
        vb.rebuildSnapshot()
    }
    // Use cached snapshot
    snapshot := vb.writeSnapshot.Load()
}
```

**Pros:** Amortizes rebuild cost across multiple reads
**Cons:** Complexity, still O(n) on rebuild

**Option C: Unified Block Cache**

Merge Writes, PendingBackendWrites, and Cache into single lookup structure.

```go
type BlockEntry struct {
    Data     []byte
    SeqNum   uint64
    State    BlockState  // Hot, Pending, Cached, Backend
}

type VB struct {
    // Single unified index for all block states
    BlockIndex *lru.Cache[uint64, BlockEntry]
}

func (vb *VB) read(block uint64, ...) {
    // Single lookup covers all cases
    if entry, ok := vb.BlockIndex.Get(block); ok {
        return entry.Data
    }
    // Only hit backend if not in unified cache
}
```

**Pros:** Single lookup path, cache-friendly, simpler code
**Cons:** Memory overhead, need eviction policy for pending writes

**Option D: Sharded Maps with RWMutex**

Replace single maps with sharded structure to reduce lock contention.

```go
const numShards = 64

type ShardedBlockMap struct {
    shards [numShards]struct {
        mu   sync.RWMutex
        data map[uint64]Block
    }
}

func (s *ShardedBlockMap) Get(block uint64) (Block, bool) {
    shard := &s.shards[block%numShards]
    shard.mu.RLock()
    defer shard.mu.RUnlock()
    b, ok := shard.data[block]
    return b, ok
}
```

**Pros:** Reduced contention, persistent structure
**Cons:** More complex, still need to manage cleanup

### Benchmarking Considerations

Before implementing larger changes, establish baseline metrics:

```bash
# Current state (with quick fix)
fio --name=randrw --ioengine=libaio --iodepth=32 --rw=randrw --rwmixread=70 \
    --bs=4k --direct=1 --size=128M --numjobs=4 --runtime=60 --group_reporting

# Profile specific operations
perf record -g -p $(pgrep -f nbdkit) -- sleep 60
perf report --stdio | head -50

# Memory allocation profiling (if pprof works)
go tool pprof -alloc_space /tmp/viperblock-mem.prof
```

### Questions to Resolve

1. **Write frequency vs read frequency**: If reads >> writes, persistent index pays off
2. **Typical pending write count**: How large do Writes.Blocks and PendingBackendWrites.Blocks get?
3. **Block locality**: Are reads typically sequential (benefits from consecutive block optimization) or random?
4. **Memory budget**: How much RAM can we dedicate to caching/indexing?
5. **Flush latency tolerance**: Can we batch index updates or must they be synchronous?

### Next Steps

1. [ ] **Benchmark quick fix** - Measure improvement from map pre-allocation
2. [ ] **Instrument pending write sizes** - Add metrics to understand typical map sizes
3. [ ] **Profile memory allocations** - Use `perf` or instrumentation to track allocation patterns
4. [ ] **Prototype Option A or C** - Implement persistent index in isolated branch
5. [ ] **Compare architectures** - Benchmark different approaches under realistic workload

### Additional Optimization Ideas

#### Memory & Allocation

| Idea | Description | Effort | Impact |
|------|-------------|--------|--------|
| **sync.Pool for slices** | Reuse `[]byte` buffers for block data instead of allocating new ones | Low | Medium |
| **sync.Pool for Block structs** | Pool frequently allocated Block objects | Low | Medium |
| **Arena allocator** | Go 1.20+ arena for batch allocations during read | Medium | High |
| **Reduce slice copies** | `writesCopy := make([]Block, len(...))` creates copies; consider read-only access patterns | Medium | Medium |

#### Data Structure Alternatives

| Structure | Use Case | Trade-offs |
|-----------|----------|------------|
| **sync.Map** | Concurrent read-heavy workloads | Slower writes, no capacity hint |
| **github.com/alphadose/haxmap** | High-performance concurrent map | External dependency |
| **github.com/cornelk/hashmap** | Lock-free concurrent map | External dependency |
| **Sorted slice + binary search** | If block numbers are dense | O(log n) lookup, O(n) insert |
| **Radix tree/trie** | Sparse block numbers | Memory efficient for ranges |
| **B-tree** | Range queries, ordered iteration | More complex, good for consecutive blocks |

#### Lock Contention

Current locking pattern in read path:
```go
vb.Writes.mu.RLock()           // Lock 1
// copy data
vb.Writes.mu.RUnlock()

vb.PendingBackendWrites.mu.RLock()  // Lock 2
// copy data
vb.PendingBackendWrites.mu.RUnlock()

vb.BlocksToObject.mu.RLock()   // Lock 3 (per block lookup)
// lookup
vb.BlocksToObject.mu.RUnlock()
```

**Ideas:**
- Combine into single lock scope if contention is high
- Use lock-free structures for read-heavy paths
- Consider RCU (Read-Copy-Update) pattern for infrequent writes

#### Zero-Copy Optimizations

```go
// Current: Multiple copies per read
writesCopy := make([]Block, len(vb.Writes.Blocks))
copy(writesCopy, vb.Writes.Blocks)  // Copy 1

data := make([]byte, blockLen)       // Alloc
copy(data[start:end], clone(wr.Data)) // Copy 2 (clone) + Copy 3

// Potential: Direct reference with careful lifetime management
// Risk: Data races if write modifies during read
```

#### CGO Overhead

The nbdkit plugin crosses CGO boundary on every I/O:
```
nbdkit (C) → CGO → Go runtime → viperblock.read() → CGO → nbdkit (C)
```

**Ideas:**
- Batch multiple block requests in single CGO call
- Consider pure-Go NBD server (bypass nbdkit entirely)
- Profile CGO overhead specifically with `go tool trace`

#### Backend I/O

If backend (predastore) becomes bottleneck again:
- **Prefetching**: Predict sequential reads, prefetch next blocks
- **Read coalescing**: Combine adjacent block reads into single request
- **Connection pooling**: Ensure HTTP/2 connections are reused
- **Compression**: Compress zero-heavy blocks

### Profiling Commands Reference

```bash
# Linux perf (works with CGO)
perf record -g -p $(pgrep -f nbdkit) -o /tmp/nbdkit.data -- sleep 60
perf report -i /tmp/nbdkit.data --stdio --no-children | head -80
perf report -i /tmp/nbdkit.data  # Interactive TUI

# Generate flamegraph
perf script -i /tmp/nbdkit.data | stackcollapse-perf.pl | flamegraph.pl > flame.svg

# Memory profiling (if Go pprof works)
curl -o /tmp/heap.prof http://localhost:6060/debug/pprof/heap
go tool pprof -top /tmp/heap.prof

# Lock contention (Go)
curl -o /tmp/mutex.prof http://localhost:6060/debug/pprof/mutex
go tool pprof -top /tmp/mutex.prof

# Trace (detailed timeline)
curl -o /tmp/trace.out http://localhost:6060/debug/pprof/trace?seconds=5
go tool trace /tmp/trace.out
```

### Performance History

| Version | Component | Issue | Fix | Result |
|---------|-----------|-------|-----|--------|
| VM-1 to VM-5 | predastore | TLS handshake per request | - | 84% CPU in TLS |
| VM-6 | predastore | HTTP/1.1 overhead | HTTP/2 + QUIC | TLS <1% CPU |
| VM-7 | viperblock | Map rebuild per read | UnifiedBlockStore | **6x improvement** |
| VM-8 | viperblock | TCP vs Socket | Unix sockets | mapassign 27%→0.03% |
| VM-9 | predastore | Bottleneck shifted | I/O bound | System optimized |
| VM-10 | s3db | Chi Logger overhead | Debug-only logging | s3db Logger disabled |
| VM-10 | viperblock | **cache_size=0 bug** | Pass to NBDKitConfig | Cache now enabled |
| VM-11 | viperblock | Cache logic inverted | Fixed HasSuffix + logic | 64MB cache for main vols |

### Related Files

- `viperblock/viperblock/viperblock.go` - Core read/write logic (lines 1688-1856 for read path)
- `viperblock/nbd/viperblock.go` - NBD plugin interface
- `predastore-rewrite/tests/hive-vm7.prof` - Predastore profile (post-HTTP2 fix)
- `/tmp/nbdkit-perf2.data` - Viperblock perf profile

### References

- [Go sync.Map documentation](https://pkg.go.dev/sync#Map)
- [Go Memory Ballast](https://blog.twitch.tv/en/2019/04/10/go-memory-ballast-how-i-learnt-to-stop-worrying-and-love-the-heap/)
- [High Performance Go Workshop](https://dave.cheney.net/high-performance-go-workshop/dotgo-paris.html)
- [Linux perf tutorial](https://perf.wiki.kernel.org/index.php/Tutorial)
- [Flamegraph](https://github.com/brendangregg/FlameGraph)

---

## VM-8: BlockStore Implementation Analysis (2026-01-26)

### Summary: BlockStore Implementation Success

| Metric | VM-1 (Baseline) | VM-7 (BlockStore) | Improvement |
|--------|-----------------|-------------------|-------------|
| **Read IOPS** | 1,143 | 6,887 | **6.0x faster** |
| **Write IOPS** | 494 | 2,980 | **6.0x faster** |
| **Read BW** | 4.5 MiB/s | 26.9 MiB/s | **6.0x faster** |
| **Write BW** | 2.0 MiB/s | 11.6 MiB/s | **5.8x faster** |
| **Read Latency** | 81 ms | 13.5 ms | **6.0x faster** |
| **Write Latency** | 70 ms | 11.6 ms | **6.0x faster** |
| **Test Duration** | 80 sec | 13 sec | **6.2x faster** |

### BlockStore Impact: Map Operations Eliminated

The `mapassign_fast64` bottleneck has been completely eliminated:

| Function | VM-7 Profile | VM-8 Profile | Reduction |
|----------|--------------|--------------|-----------|
| `runtime.mapassign_fast64` | **27.45%** | **0.02%** | **99.93%** |
| `runtime.mapaccess2_fast64` | ~3.2% | 0.24% | **92.5%** |
| `maps.(*table).rehash` | ~6.4% | 0% | **100%** |
| Total map overhead | ~50% | <1% | **>98%** |

### Current Profile Analysis (VM-8)

**NBDKit/Viperblock Client (nbdkit-perf3.data):**

| Function | CPU % | Category |
|----------|-------|----------|
| `reflect.StructTag.Lookup` | 2.93% | AWS SDK v1 reflection |
| `runtime.memmove` | 2.64% | Memory copying |
| `runtime.memclrNoHeapPointers` | 1.28% | Memory clearing |
| `runtime.mallocgcSmallScanNoHeader` | 1.27% | GC allocations |
| `runtime.selectgo` | 1.04% | Channel operations |
| `crypto/md5.block` | 0.83% | AWS v4 signatures |
| `crypto/sha256` | 0.58% | TLS crypto |

**Predastore Server (hive-vm8.prof):**

| Function | CPU % | Category |
|----------|-------|----------|
| `syscall.Syscall6` | 36.53% | **I/O bound** (good!) |
| `io.ReadAtLeast` | 23.31% | Disk reads |
| `handleGET` | 27.22% | Request handling |
| `hash/crc32` | 5.59% | Data integrity |
| `encoding/gob` | ~8% | Distributed backend |

### Key Finding: System is Now I/O Bound

The predastore server is spending 36% of CPU time in syscalls - this indicates the system is now **I/O bound** rather than CPU bound. The read path flows through:

```
syscall.Syscall6 (36%) -> io.ReadAtLeast (23%) -> os.File.Read (20%)
```

This is the expected profile for a well-optimized storage system.

---

## Recommendations for Further Performance Gains

### Priority 1: AWS SDK v1 -> v2 Migration (Medium Impact)

**Current overhead:** 2.93% in `reflect.StructTag.Lookup`

AWS SDK v1 uses reflection heavily for XML/REST parsing. SDK v2 uses code generation instead.

```go
// Before: viperblock/backends/s3/s3.go using aws-sdk-go v1
import "github.com/aws/aws-sdk-go/service/s3"

// After: Migrate to aws-sdk-go-v2
import "github.com/aws/aws-sdk-go-v2/service/s3"
```

**Expected improvement:** ~3% CPU reduction, better memory efficiency

### Priority 2: Request Batching / Read-Ahead (High Impact)

The profile shows heavy request overhead even with HTTP/2. With 131K operations in the test, that's still 131K individual S3 GetObject calls.

**Recommendation:** Batch adjacent block requests into single range reads.

```go
// Current: One S3 request per 4KB block
func (b *Backend) Read(fileType, objectID, offset, length) []byte

// Proposed: Coalesce adjacent blocks
func (b *Backend) ReadRange(fileType, objectID, startOffset, endOffset) []byte
```

**Expected improvement:** 2-5x for sequential workloads

### Priority 3: Reduce CRC32 Overhead (Low-Medium Impact)

**Current overhead:** 5.59% in `hash/crc32` on predastore server

For trusted internal traffic, consider:
- Making CRC optional via config flag
- Computing CRC in parallel with I/O

### Priority 4: Replace Gob with Protocol Buffers (Medium Impact)

**Current overhead:** ~8% in `encoding/gob` on predastore

Gob is Go-specific and uses reflection. Protocol Buffers or FlatBuffers would be faster.

### Priority 5: Memory Pool for Block Buffers (Low Impact)

Add `sync.Pool` for frequently allocated buffers:

```go
var blockPool = sync.Pool{
    New: func() interface{} {
        return make([]byte, 4096)
    },
}

// In read path
buf := blockPool.Get().([]byte)
defer blockPool.Put(buf)
```

---

## Performance Ceiling Analysis

### Theoretical Maximum

| Component | Theoretical Limit | Current | Gap |
|-----------|------------------|---------|-----|
| Host NVMe | 73,400 IOPS | 6,887 IOPS | 10.7x |
| Network (10 Gbps) | ~300K IOPS @ 4KB | 6,887 IOPS | 43x |

The host can do 73K IOPS directly. The gap indicates there's still significant protocol overhead from:
1. QUIC/HTTP2 framing
2. S3 protocol overhead (XML parsing, signatures)
3. Predastore distributed backend coordination

### Achievable Target

With recommended optimizations:
- **Short term (batching):** 15,000-20,000 IOPS
- **Medium term (SDK v2 + protobuf):** 25,000-35,000 IOPS
- **Long term (custom protocol):** 50,000+ IOPS

---

## Next Steps

- [ ] Validate BlockStore stability - Run extended tests to ensure no regressions
- [ ] Implement read batching - Coalesce adjacent blocks for sequential workloads
- [ ] Migrate to AWS SDK v2 - Eliminate reflection overhead
- [ ] Profile write path - Ensure write performance is similarly optimized
- [ ] Add metrics - Instrument IOPS, latency percentiles, cache hit rates

---

## Conclusion

The BlockStore implementation was a major success, reducing map overhead from ~50% to <1% and delivering a **6x performance improvement**. The system is now I/O bound, which is the optimal state for a storage system. Further gains will come from reducing protocol overhead (SDK v2, batching) rather than algorithmic improvements.

---

## VM-9: Unix Socket Analysis (2026-01-26)

### VM-7 vs VM-8 Comparison (TCP vs Unix Socket)

| Metric | VM-7 (TCP) | VM-8 (Socket) | Change |
|--------|------------|---------------|--------|
| **Read IOPS** | 6,887 | 6,815 | -1.0% |
| **Write IOPS** | 2,980 | 2,948 | -1.1% |
| **Read Latency** | 13.54 ms | 13.71 ms | +1.3% |
| **Write Latency** | 11.60 ms | 11.71 ms | +0.9% |
| **Test Duration** | 13.28 sec | 13.42 sec | similar |

### Key Observation: Bottleneck Shifted to Predastore

VM-level numbers are nearly identical because **the bottleneck moved from nbdkit to predastore**. This is confirmed by:
- htop showing predastore as the CPU-bound process
- nbdkit profile showing well-distributed CPU usage

**This is good news:** nbdkit/viperblock optimizations (BlockStore + Unix sockets) are working - nbdkit is now waiting on predastore.

### NBDKit Profile (nbdkit-perf4.data) - BlockStore Confirmed

| Function | VM-7 Profile | VM-9 Profile | Reduction |
|----------|--------------|--------------|-----------|
| `runtime.mapassign_fast64` | 27.45% | **0.03%** | **99.9%** |
| Top function | 27.45% | 3.05% | Well-distributed |

Profile is now evenly distributed:
- `reflect.StructTag.Lookup`: 3.05% (AWS SDK v1)
- `runtime.memmove`: 2.73%
- `runtime.memclrNoHeapPointers`: 1.32%
- All other functions < 1.5%

### Predastore Profile (hive-vm9.prof) - Now the Bottleneck

| Function | CPU % | Category |
|----------|-------|----------|
| `syscall.Syscall6` | **37.28%** | I/O operations |
| `hash/crc32.ieeeCLMUL` | **5.33%** | CRC32 checksums |
| `encoding/gob.*` | **~8-10%** | Gob serialization |
| `io.ReadAtLeast` | 23.41% cum | File reading |
| `handleGET.func1` | 27.24% cum | Request handler |

**System is now I/O bound on predastore** - we're hitting storage limits.

### Logging Optimization

High-frequency logs changed from Info to Debug level:
- `s3db/server.go`: Put/Delete succeeded logs
- `quic/quicserver/server.go`: handlePUTShard/handleDELETEShard logs

These logs were firing on every operation, adding unnecessary overhead.

### Next Optimizations (Predastore-Focused)

1. **Gob Serialization (~8-10%)** - Replace with Protocol Buffers
2. **CRC32 Checksums (5.33%)** - Make optional for trusted networks
3. **Request Batching** - Coalesce adjacent block reads
4. **AWS SDK v2 Migration** - Eliminate reflection overhead (3.05%)

### Performance Ceiling Update

| Component | Theoretical | VM-1 | VM-9 | Gap to Theoretical |
|-----------|-------------|------|------|-------------------|
| Host NVMe | 73,400 IOPS | 1,143 | 6,815 | 10.8x |
| Progress | - | Baseline | **6x faster** | - |

### Remaining Bottlenecks

1. **I/O Bound (37%)** - Expected, indicates storage limit
2. **Gob Encoding (~10%)** - Avoidable with protobuf
3. **CRC32 (5.33%)** - Potentially skippable
4. **AWS SDK v1 Reflection (3.05%)** - Migrate to SDK v2

---

## VM-10: Deep Profile Analysis - Chi Logger & HTTP/2 vs QUIC (2026-01-26)

### Chi Middleware Logger Overhead

**Problem Identified:** The Go profile (hive-vm9.prof) showed significant overhead from chi's RequestLogger middleware:

```
chi/v5/middleware.init.0.RequestLogger.func1.1  27.47s  17.39%
```

This middleware logs every HTTP request and was firing 130K+ times during the performance test.

### Fix Applied: Conditional Request Logging

**File: `predastore-rewrite/s3db/server.go`**

```go
// ServerConfig - Added Debug flag
type ServerConfig struct {
    // ... existing fields ...

    // Debug enables verbose request logging (chi middleware.Logger)
    // WARNING: Enabling this in production adds significant CPU overhead (~17%)
    Debug bool
}

// Middleware setup - Now conditional
func NewServer(config *ServerConfig) (*Server, error) {
    // Only enable request logging in debug mode - adds ~17% CPU overhead
    if config.Debug {
        s.router.Use(middleware.Logger)
    }
    s.router.Use(middleware.Recoverer)
    s.router.Use(s.authMiddleware)
}
```

**Expected impact:** ~17% CPU reduction when Debug=false (default)

### HTTP/2 vs QUIC Analysis for s3db

**Current Architecture:**
- **s3db** uses HTTP/2 over TLS for metadata operations (Raft consensus, key-value store)
- **QUIC server** handles bulk shard data transfers

**Profile breakdown for s3db HTTP/2:**
| Component | CPU % | Purpose |
|-----------|-------|---------|
| chi Logger | 17.39% | Request logging (FIXED) |
| CORS Middleware | 12.84% | Cross-origin handling |
| SigV4 Auth | 12.78% | AWS signature validation |
| Auth Middleware | 2.91% | s3db auth check |
| **Total HTTP/2 overhead** | ~46% | Protocol stack |

**Profile for QUIC server (shards):**
- Handles bulk of actual I/O data
- More efficient for stream-based transfers
- Already optimized with HTTP/3 semantics

### Should s3db Move to QUIC?

**Arguments for QUIC migration:**
1. **0-RTT connection establishment** - Faster reconnects
2. **Built-in multiplexing** - No head-of-line blocking
3. **Stream-based** - Better for many small requests
4. **Already have QUIC infrastructure** - Reuse existing quic-go setup

**Arguments against QUIC migration:**
1. **Hashicorp Raft** - Uses HTTP transport internally for leader election and log replication
2. **Complexity** - Would need custom QUIC transport layer for Raft
3. **Diminishing returns** - With Logger disabled, HTTP/2 overhead is now ~29%

### Recommendation

**Short term (High Impact, Low Effort):**
1. ✅ Disable chi Logger middleware (17% gain) - **DONE**
2. ✅ Disable per-request success logs (Put/Delete succeeded) - **DONE**
3. Consider removing CORS middleware for internal traffic - 12.84% potential gain

**Medium term (Medium Impact, Medium Effort):**
1. Optimize SigV4 auth - Cache signature computations for same client
2. Consider pre-authenticated connections for internal services

**Long term (High Impact, High Effort):**
1. Custom QUIC transport for s3db (requires Raft transport implementation)
2. Replace s3db with QUIC-native distributed store

### Updated Remaining Bottlenecks (Post VM-10 Fix)

| Component | Before | After | Savings |
|-----------|--------|-------|---------|
| chi Logger | 17.39% | 0% | 17.39% |
| CORS | 12.84% | 12.84% | - |
| SigV4 Auth | 12.78% | 12.78% | - |
| Syscalls (I/O) | 37.28% | ~45% | (shifts up) |

With Logger disabled, the profile should shift to show I/O as an even larger percentage, confirming the system is properly I/O bound.

### Files Changed

| File | Change |
|------|--------|
| `s3db/server.go` | Added Debug flag, conditional middleware.Logger |
| `s3db/server.go` | Put/Delete logs changed to slog.Debug |
| `quic/quicserver/server.go` | Shard logs changed to slog.Debug |

### Verification

After deploying, run benchmark and verify:

```bash
# Profile should show no chi Logger overhead
go tool pprof -top hive-vm10.prof | grep -i logger
# Expected: No results

# I/O should now dominate
go tool pprof -top hive-vm10.prof | head -10
# Expected: syscall.Syscall6 > 45%
```

---

## VM-10: Benchmark Results (2026-01-26)

### Profile Analysis

**Predastore (hive-vm10.prof):**
| Function | CPU % | Notes |
|----------|-------|-------|
| `syscall.Syscall6` | **37.32%** | I/O bound - expected |
| `handleGET.func1` | 26.59% cum | QUIC handler |
| `io.ReadAtLeast` | 23.32% cum | File I/O |
| `chi/v5/middleware.RequestLogger` | **13.22%** | S3 HTTP server (NOT fixed) |
| `sigV4AuthMiddleware` | 12.41% | AWS auth |
| `corsMiddleware` | 12.46% | CORS |
| `hash/crc32.ieeeCLMUL` | 5.13% | Checksums |
| `encoding/gob.compileDec` | 3.74% | Gob serialization |

**Finding: S3 HTTP Server Logger Still Active**

The chi Logger middleware is still running on the main S3 HTTP server (13.22% overhead). The s3db fix worked, but the S3 server has a separate `DisableLogging` config that wasn't set.

**NBDKit/Viperblock (nbdkit-perf5.data):**
| Function | CPU % | Notes |
|----------|-------|-------|
| `reflect.StructTag.Lookup` | 2.96% | AWS SDK v1 reflection |
| `runtime.memmove` | 2.62% | Memory copies |
| `runtime.memclrNoHeapPointers` | 1.34% | Memory clearing |
| `runtime.mallocgcSmallScanNoHeader` | 1.27% | GC allocation |
| `runtime.selectgo` | 1.14% | Channel operations |
| `runtime.mapassign_fast64` | **0.02%** | BlockStore working! |

**BlockStore Success Confirmed:**
- `mapassign_fast64` at 0.02% (down from 27.45% in VM-7)
- Profile is now evenly distributed - no single bottleneck
- Top function only 2.96% (was 27.45%)

### Critical Bug Found: cache_size=0

**Bug:** NBDKit launched with `cache_size=0` for ALL volumes:
```
nbdkit ... cache_size=0
```

**Root Cause:** The `NBDKitConfig.CacheSize` was never set in viperblockd.go. The `vb.SetCacheSize()` was called on the Go viperblock instance, but nbdkit creates a SEPARATE viperblock instance in the CGO plugin.

**Fix Applied:** Added `CacheSize: nbdCacheSize` to `NBDKitConfig` struct initialization:
```go
nbdConfig := nbd.NBDKitConfig{
    // ... other fields ...
    CacheSize:  nbdCacheSize,  // Added - was missing!
}
```

### Performance Impact

**Without cache (VM-10):** All reads go to predastore backend
**With cache (VM-11):** Hot blocks served from 64MB LRU cache

Expected improvement for workloads with locality (VM boot, repeated reads):
- Cache hit rate 60-80% for sequential/repeated access
- 2-5x fewer backend requests
- Reduced predastore load

### Next Steps

1. **Fix S3 HTTP Server logging** - Set `DisableLogging: true` in config
2. **Re-run benchmark** with cache properly enabled (VM-11)
3. **Consider AWS SDK v2** - 2.96% overhead from reflection

---

## VM-11: Viperblock LRU Cache Fix & Analysis (2026-01-26)

### Critical Bugs Fixed

**1. `strings.HasSuffix` Arguments Were Backwards**

```go
// BEFORE (WRONG - condition always evaluates incorrectly):
if !strings.HasSuffix("-cloudinit", ebsRequest.Name) ...

// AFTER (CORRECT):
if strings.HasSuffix(ebsRequest.Name, "-cloudinit") ...
```

The Go `strings.HasSuffix(s, suffix)` function checks if string `s` ends with `suffix`. The arguments were swapped, causing the condition to always be true for main volumes.

**2. Cache Enable/Disable Logic Was Inverted**

```go
// BEFORE (WRONG - disabled cache for main volumes!):
if !strings.HasSuffix("-cloudinit", ebsRequest.Name) && !strings.HasSuffix("-efi", ebsRequest.Name) {
    slog.Info("Disabling cache for volume", "volume", ebsRequest.Name)
    vb.SetCacheSystemMemory(0)  // DISABLED cache for main volumes
} else {
    vb.SetCacheSize(defaultCache, 0)  // ENABLED cache for cloudinit/efi
}

// AFTER (CORRECT - enables cache for main volumes):
if strings.HasSuffix(ebsRequest.Name, "-cloudinit") || strings.HasSuffix(ebsRequest.Name, "-efi") {
    slog.Info("Disabling cache for auxiliary volume", "volume", ebsRequest.Name)
    vb.SetCacheSize(0, 0)  // Disable for small auxiliary volumes
} else {
    slog.Info("Enabling 64MB cache for main volume", "volume", ebsRequest.Name, "blocks", defaultCache)
    vb.SetCacheSize(defaultCache, 0)  // Enable 64MB cache for main volumes
}
```

**Impact:** Main volumes were running with NO cache, while small cloudinit/efi volumes had caching enabled. This was backwards.

### Cache Configuration

**64MB Cache Size Calculation:**
```
Cache memory:  64 * 1024 * 1024 = 67,108,864 bytes
Block size:    4,096 bytes
Cache entries: 67,108,864 / 4,096 = 16,384 blocks
```

The LRU cache can hold 16,384 most-recently-used 4KB blocks in memory.

### Read Path Logging Optimization

Changed 8 `slog.Info` statements to `slog.Debug` in the viperblock read path:

| Line | Log Message | Frequency |
|------|-------------|-----------|
| 1798 | `[READ] ZERO BLOCK` | Per zero block |
| 1804 | `[READ] OBJECT ID` | Per backend lookup |
| 1825 | `[READ] CONSECUTIVE BLOCK` | Per consecutive block group |
| 1829 | `[READ] SKIPPING CONSECUTIVE BLOCK READ` | Per skip |
| 1860 | `[READ] READING CONSECUTIVE BLOCK` | Per backend fetch |
| 1874 | `[READ] COPYING BLOCK DATA` | Per copy operation |
| 1876 | `[READ] DATA` | Per read operation |
| 2230 | `[READ] READING CONSECUTIVE BLOCK` | Per BlockStore fetch |

### Cache Architecture Analysis

**Current Implementation:**

```
Read Request
    │
    ├─► Check BlockStore (Hot/Pending/Cached states)
    │       └─► If BlockStateCached → return data (hit)
    │
    ├─► Check Legacy LRU cache (hashicorp/lru)
    │       └─► If hit → return data
    │
    └─► Fetch from Backend (predastore)
            │
            ├─► BlockStore.Cache(block, data)     ← Caches in BlockStore
            └─► vb.Cache.lru.Add(block, data)     ← ALSO caches in LRU (duplicate!)
```

**Issue: Duplicate Caching**

Data fetched from backend is cached in BOTH:
1. `BlockStore` (as `BlockStateCached` entries)
2. `hashicorp/lru` cache

This wastes memory by storing the same data twice.

**Issue: No BlockStore Cache Eviction**

The `UnifiedBlockStore` has `EvictCache()` method but no automatic LRU eviction logic. `BlockStateCached` entries accumulate without bound. Only the hashicorp LRU has automatic eviction (capped at 16,384 entries).

### Files Changed

| File | Change |
|------|--------|
| `hive/services/viperblockd/viperblockd.go` | Fixed HasSuffix args, fixed cache logic |
| `viperblock/viperblock/viperblock.go` | 8 read logs changed to Debug level |

### Expected Impact (VM-11)

With 64MB cache properly enabled for main volumes:
- **Cache hits**: Repeated reads of same blocks avoid backend I/O
- **Reduced latency**: Memory access (~100ns) vs network I/O (~1ms)
- **Reduced predastore load**: Fewer GET requests to backend

**Best case scenario (high locality workload):**
- Boot sequence reads same blocks repeatedly → high cache hit rate
- Could see 2-5x improvement for read-heavy workloads

**Worst case scenario (random I/O, no locality):**
- Each read is unique block → 0% cache hit rate
- No improvement, but no regression either

### Future Optimizations

**Priority 1: Eliminate Duplicate Caching**

Choose ONE caching strategy:
```go
// Option A: Use only hashicorp LRU (simpler)
if vb.Cache.Config.Size > 0 {
    vb.Cache.lru.Add(currentBlock, blockData)
}
// Remove: vb.BlockStore.Cache(...)

// Option B: Add LRU eviction to BlockStore (more integrated)
type UnifiedBlockStore struct {
    cachedLRU  *lru.Cache[uint64, struct{}]  // Track cache order
    maxCached  int                            // Max cached entries
}
```

**Priority 2: Read-Ahead / Prefetching**

For sequential workloads, prefetch next N blocks:
```go
func (vb *VB) readWithPrefetch(block uint64, blockLen uint64) {
    // Detect sequential access pattern
    if block == vb.lastBlock + 1 {
        vb.sequentialCount++
        if vb.sequentialCount > threshold {
            // Prefetch next 8 blocks in background
            go vb.prefetch(block + blockRequests, 8)
        }
    }
}
```

**Priority 3: Adaptive Cache Sizing**

Adjust cache size based on workload:
```go
// Monitor hit rate
hitRate := cacheHits / (cacheHits + cacheMisses)
if hitRate < 0.5 && cacheSize < maxCache {
    // Low hit rate, increase cache
    vb.SetCacheSize(cacheSize * 2)
} else if hitRate > 0.95 && cacheSize > minCache {
    // Very high hit rate, could reduce cache
}
```

### Verification

```bash
# Check cache is enabled for main volumes
grep "Enabling 64MB cache" ~/hive/logs/viperblockd.log
# Expected: One line per main volume

# Check cache disabled for auxiliary volumes
grep "Disabling cache for auxiliary" ~/hive/logs/viperblockd.log
# Expected: Lines for *-cloudinit and *-efi volumes

# Monitor cache hit rate (if stats endpoint available)
# Or add temporary logging to track hits/misses
```

### Cache Hit Rate Expectations

| Workload Type | Expected Hit Rate | Notes |
|---------------|-------------------|-------|
| VM Boot | 60-80% | Repeated reads of kernel, initrd |
| Database (OLTP) | 20-40% | Hot pages cached |
| Sequential scan | 0-10% | Each block read once |
| Random I/O | 5-15% | Depends on working set size |

The 64MB cache (16K blocks) is most effective when the working set fits in cache.