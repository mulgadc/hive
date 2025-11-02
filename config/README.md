# Hive Systemd Service

This directory contains the systemd service configuration for running Hive services on Ubuntu and Debian systems.

## Files

- `hive.service` - The systemd service file that manages all Hive services
- `install-hive-service.sh` - Installation script to set up the service
- `README.md` - This documentation file

## Installation

### Prerequisites

1. Ensure Hive is installed in `/opt/hive/`
2. Ensure the `hive` binary is available at `/opt/hive/bin/hive`
3. Run the installation script as root

### Setup Configuration

```
/opt/hive/bin/hive admin init
```

This will init the Hive environment and create the default configuration files in the home directory, e.g `~/hive/`.

```
/home/username/hive:
amis  config  images  logs  nats  predastore  state  viperblock  volumes

/home/username/hive/amis:

/home/username/hive/config:
awsgw  hive.toml  nats  predastore  server.key  server.pem

/home/username/hive/config/awsgw:
awsgw.toml

/home/username/hive/config/nats:
nats.conf

/home/username/hive/config/predastore:
predastore.toml

/home/username/hive/images:

/home/username/hive/logs:
awsgw.log  awsgw.pid  hive.log  hive.pid  nats.log  nats.pid  predastore.log  predastore.pid  viperblock.log  viperblock.pid

/home/username/hive/nats:

/home/username/hive/predastore:
data

/home/username/hive/predastore/data:
predastore

/home/ben/hive/state:

/home/ben/hive/viperblock:

/home/ben/hive/volumes:
```

### Quick Installation

```bash
sudo ./config/install-hive-service.sh
```

### Manual Installation

If you prefer to install manually:

1. Copy the service file:
   ```bash
   sudo cp config/hive.service /etc/systemd/system/
   ```

2. Reload systemd:
   ```bash
   sudo systemctl daemon-reload
   ```

3. Enable the service:
   ```bash
   sudo systemctl enable hive.service
   ```

## Service Management

### Start the service
```bash
sudo systemctl start hive
```

### Stop the service
```bash
sudo systemctl stop hive
```

### Check service status
```bash
sudo systemctl status hive
```

### Restart the service
```bash
sudo systemctl restart hive
```

### View service logs
```bash
sudo journalctl -u hive -f
```

### Disable auto-start
```bash
sudo systemctl disable hive
```

## Service Details

The `hive.service` file:

- Starts services in the correct order: NATS → Predastore → Viperblock
- Includes proper delays between service starts to ensure dependencies are ready
- Stops services in reverse order during shutdown
- Runs as root (required for system services)
- Includes security hardening settings
- Provides proper logging to systemd journal
- Automatically restarts on failure

## Service Order

The service starts the following components in order:

1. **NATS** - Message broker service
2. **Predastore** - Data storage service (waits 3 seconds after NATS)
3. **Viperblock** - Block storage service (waits 2 seconds after Predastore)

## Troubleshooting

### Check if the service is running
```bash
sudo systemctl is-active hive
```

### View recent logs
```bash
sudo journalctl -u hive --since "1 hour ago"
```

### Check service configuration
```bash
sudo systemctl cat hive
```

### Verify binary exists
```bash
ls -la /opt/hive/bin/hive
```

### Test service commands manually
```bash
/opt/hive/bin/hive service nats start
/opt/hive/bin/hive service predastore start
/opt/hive/bin/hive service viperblock start
```

## Customization

If you need to modify the service configuration:

1. Edit the service file: `sudo nano /etc/systemd/system/hive.service`
2. Reload systemd: `sudo systemctl daemon-reload`
3. Restart the service: `sudo systemctl restart hive`

## Security Notes

- The service runs as root, which is necessary for system-level services
- Security hardening is enabled with `ProtectSystem=strict` and `NoNewPrivileges=true`
- Only specific paths are writable: `/opt/hive`, `/var/log`, `/var/run` 