# Distributed StartInstances - Manual Testing Guide

Manual test plan for verifying stop/start instance lifecycle across daemon nodes, including daemon restarts. Requires a multi-node cluster (see INSTALL.md "Multi-node Configuration").

## Prerequisites

```bash
# Build
make build

# Set up 3-node cluster (if not already running)
./scripts/create-multi-node.sh
./scripts/start-dev.sh ~/node1/
./scripts/start-dev.sh ~/node2/
./scripts/start-dev.sh ~/node3/

# AWS CLI setup
export AWS_PROFILE=hive
```

Pick a gateway endpoint for all commands (any node works):

```bash
export EP="--endpoint-url https://10.11.12.1:9999"
```

Import an SSH key and AMI if not already done (see INSTALL.md).

---

## Test 1: Basic Stop and Start (Same Cluster)

Verify an instance can be stopped and restarted by any daemon.

```bash
# 1. Launch an instance
aws $EP ec2 run-instances \
  --image-id $HIVE_AMI \
  --instance-type $HIVE_INSTANCE \
  --key-name hive-key \
  --count 1

export INSTANCE_ID="i-XXX"  # from output
```

```bash
# 2. Confirm running
aws $EP ec2 describe-instances --instance-ids $INSTANCE_ID
# Expect: State.Name = "running", State.Code = 16
```

```bash
# 3. Stop the instance
aws $EP ec2 stop-instances --instance-ids $INSTANCE_ID
# Expect: PreviousState.Name = "running", CurrentState.Name = "stopping"
```

```bash
# 4. Wait for stop to complete, then verify stopped
aws $EP ec2 describe-instances --instance-ids $INSTANCE_ID
# Expect: State.Name = "stopped", State.Code = 80
```

```bash
# 5. Check daemon logs on the original node — should show KV release
grep -i "Released stopped instance ownership" ~/node*/logs/hive.log
# Expect: "Released stopped instance ownership to shared KV" with instanceId and lastNode
```

```bash
# 6. Start the instance
aws $EP ec2 start-instances --instance-ids $INSTANCE_ID
# Expect: PreviousState.Name = "stopped", CurrentState.Name = "pending"
```

```bash
# 7. Confirm running again
aws $EP ec2 describe-instances --instance-ids $INSTANCE_ID
# Expect: State.Name = "running", State.Code = 16
```

```bash
# 8. Check which node picked up the start
grep -i "Started stopped instance from shared KV" ~/node*/logs/hive.log
# Note the node — it may differ from the original
```

---

## Test 2: Stop, Restart Daemon, Then Start

Verify a stopped instance survives a daemon restart and can be started after.

```bash
# 1. Launch and stop an instance (repeat Test 1 steps 1-5)
export INSTANCE_ID="i-XXX"
```

```bash
# 2. Identify which node originally ran the instance
grep "Released stopped instance" ~/node*/logs/hive.log | grep $INSTANCE_ID
# Example: ~/node1/logs/hive.log — node1 was the original
```

```bash
# 3. Restart that daemon
./scripts/stop-dev.sh ~/node1/
./scripts/start-dev.sh ~/node1/
```

```bash
# 4. Verify the instance is still visible as stopped
aws $EP ec2 describe-instances --instance-ids $INSTANCE_ID
# Expect: State.Name = "stopped"
# The instance is in shared KV, so any running daemon can report it
```

```bash
# 5. Start the instance
aws $EP ec2 start-instances --instance-ids $INSTANCE_ID
# Expect: PreviousState.Name = "stopped", CurrentState.Name = "pending"
```

```bash
# 6. Confirm running and check which node picked it up
aws $EP ec2 describe-instances --instance-ids $INSTANCE_ID
grep "Started stopped instance from shared KV" ~/node*/logs/hive.log | grep $INSTANCE_ID
```

---

## Test 3: Stop on Node A, Kill Node A, Start on Node B

Verify a stopped instance can be started when its original daemon is down.

```bash
# 1. Launch instance (it lands on some node)
aws $EP ec2 run-instances \
  --image-id $HIVE_AMI \
  --instance-type $HIVE_INSTANCE \
  --key-name hive-key \
  --count 1

export INSTANCE_ID="i-XXX"
```

```bash
# 2. Stop the instance
aws $EP ec2 stop-instances --instance-ids $INSTANCE_ID
```

```bash
# 3. Wait for stopped state
aws $EP ec2 describe-instances --instance-ids $INSTANCE_ID
# Expect: State.Name = "stopped"
```

```bash
# 4. Identify the original node and shut it down
grep "Released stopped instance" ~/node*/logs/hive.log | grep $INSTANCE_ID
# If node1 — stop node1
./scripts/stop-dev.sh ~/node1/
```

```bash
# 5. Point CLI to a surviving node and start the instance
export EP="--endpoint-url https://10.11.12.2:9999"
aws $EP ec2 start-instances --instance-ids $INSTANCE_ID
# Expect: CurrentState.Name = "pending" — a different daemon picked it up
```

```bash
# 6. Confirm running on the new node
aws $EP ec2 describe-instances --instance-ids $INSTANCE_ID
# Expect: State.Name = "running"
grep "Started stopped instance from shared KV" ~/node2/logs/hive.log ~/node3/logs/hive.log
```

```bash
# 7. Bring node1 back up (cleanup)
./scripts/start-dev.sh ~/node1/
```

---

## Test 4: Repeated Stop/Start Cycles

Verify the instance can be stopped and started multiple times without state corruption.

```bash
# 1. Launch an instance
aws $EP ec2 run-instances \
  --image-id $HIVE_AMI \
  --instance-type $HIVE_INSTANCE \
  --key-name hive-key \
  --count 1

export INSTANCE_ID="i-XXX"
```

```bash
# 2. Cycle 3 times
for i in 1 2 3; do
  echo "=== Cycle $i ==="

  # Wait for running
  sleep 5
  aws $EP ec2 describe-instances --instance-ids $INSTANCE_ID \
    --query 'Reservations[0].Instances[0].State.Name' --output text
  # Expect: running

  # Stop
  aws $EP ec2 stop-instances --instance-ids $INSTANCE_ID
  sleep 10

  aws $EP ec2 describe-instances --instance-ids $INSTANCE_ID \
    --query 'Reservations[0].Instances[0].State.Name' --output text
  # Expect: stopped

  # Start
  aws $EP ec2 start-instances --instance-ids $INSTANCE_ID
  sleep 10
done
```

```bash
# 3. Final state check
aws $EP ec2 describe-instances --instance-ids $INSTANCE_ID
# Expect: State.Name = "running"
```

---

## Test 5: DescribeInstances Includes Stopped Instances

Verify stopped instances appear alongside running ones in describe output.

```bash
# 1. Launch two instances
aws $EP ec2 run-instances \
  --image-id $HIVE_AMI \
  --instance-type $HIVE_INSTANCE \
  --key-name hive-key \
  --count 1
export RUNNING_ID="i-XXX"

aws $EP ec2 run-instances \
  --image-id $HIVE_AMI \
  --instance-type $HIVE_INSTANCE \
  --key-name hive-key \
  --count 1
export STOPPED_ID="i-YYY"
```

```bash
# 2. Stop one, keep the other running
aws $EP ec2 stop-instances --instance-ids $STOPPED_ID
sleep 10
```

```bash
# 3. Describe all instances — both should appear
aws $EP ec2 describe-instances
# Expect: $RUNNING_ID with State.Name = "running"
# Expect: $STOPPED_ID with State.Name = "stopped"
```

```bash
# 4. Describe with instance ID filter
aws $EP ec2 describe-instances --instance-ids $STOPPED_ID
# Expect: only the stopped instance
```

---

## Test 6: Start a Non-Existent Instance

Verify proper error handling.

```bash
aws $EP ec2 start-instances --instance-ids i-doesnotexist
# Expect: instance remains reported as stopped (state unchanged)
# Check daemon logs for "instance not found in shared KV"
```

---

## Test 7: Start an Already Running Instance

Verify that starting a running instance does not cause issues.

```bash
# 1. Launch and confirm running
aws $EP ec2 run-instances \
  --image-id $HIVE_AMI \
  --instance-type $HIVE_INSTANCE \
  --key-name hive-key \
  --count 1
export INSTANCE_ID="i-XXX"
sleep 5
```

```bash
# 2. Try to start it again
aws $EP ec2 start-instances --instance-ids $INSTANCE_ID
# Expect: instance not found in shared KV (running instances are not in shared KV)
# The response should report the instance as still "stopped" (no-op from gateway perspective)
```

---

## Test 8: Daemon Crash During Stop (Migration Recovery)

Verify that a daemon crash between KV write and local cleanup is handled on restart.

```bash
# 1. Launch and stop an instance
aws $EP ec2 run-instances \
  --image-id $HIVE_AMI \
  --instance-type $HIVE_INSTANCE \
  --key-name hive-key \
  --count 1
export INSTANCE_ID="i-XXX"

aws $EP ec2 stop-instances --instance-ids $INSTANCE_ID
sleep 10
```

```bash
# 2. Force kill the daemon process (simulate crash — skips cleanup)
# Identify the daemon PID on the node that ran the instance
grep "Released stopped instance" ~/node*/logs/hive.log | grep $INSTANCE_ID
# If node1:
kill -9 $(pgrep -f "hive.*node1")
```

```bash
# 3. Restart the daemon
./scripts/start-dev.sh ~/node1/
```

```bash
# 4. Check logs for migration handling
grep -i "Migrated stopped instance" ~/node1/logs/hive.log
# If the instance was still in per-node state, restoreInstances migrates it to shared KV
```

```bash
# 5. Verify instance is still startable
aws $EP ec2 start-instances --instance-ids $INSTANCE_ID
aws $EP ec2 describe-instances --instance-ids $INSTANCE_ID
# Expect: State.Name = "running"
```

---

## What to Look For in Logs

Key log messages and where to find them (`~/node*/logs/hive.log`):

| Log Message | When |
|-------------|------|
| `Released stopped instance ownership to shared KV` | After successful stop + KV write |
| `Started stopped instance from shared KV` | After a daemon picks up and launches a stopped instance |
| `Migrated stopped instance to shared KV` | During `restoreInstances` on daemon startup |
| `Failed to write stopped instance to shared KV, keeping local ownership` | KV write failed, instance stays local |
| `instance not found in shared KV` | Start request for non-existent instance |
| `instance not in stopped state` | Start request for instance not in stopped state |
| `DescribeInstances: Collected stopped instance reservations` | Gateway merged stopped instances into describe output |
