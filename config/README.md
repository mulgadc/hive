# Spinifex Systemd Service

This directory contains the systemd service configuration for running Spinifex services on Ubuntu and Debian systems.

## Files

- `spinifex.service` - The systemd service file that manages all Spinifex services
- `install-spinifex-service.sh` - Installation script to set up the service
- `README.md` - This documentation file

## Installation

### Prerequisites

1. Ensure Spinifex is installed in `/opt/spinifex/`
2. Ensure the `spx` binary is available at `/opt/spinifex/bin/spx`
3. Run the installation script as root

### Setup Configuration

```
/opt/spinifex/bin/spx admin init
```

This will init the Spinifex environment and create the default configuration files in the home directory, e.g `~/spinifex/`.

```
/home/username/spinifex:
amis  config  images  logs  nats  predastore  state  viperblock  volumes

/home/username/spinifex/amis:

/home/username/spinifex/config:
awsgw  spinifex.toml  nats  predastore  server.key  server.pem

/home/username/spinifex/config/awsgw:
awsgw.toml

/home/username/spinifex/config/nats:
nats.conf

/home/username/spinifex/config/predastore:
predastore.toml

/home/username/spinifex/images:

/home/username/spinifex/logs:
awsgw.log  awsgw.pid  spinifex.log  spinifex.pid  nats.log  nats.pid  predastore.log  predastore.pid  viperblock.log  viperblock.pid

/home/username/spinifex/nats:

/home/username/spinifex/predastore:
data

/home/username/spinifex/predastore/data:
predastore

/home/ben/spinifex/state:

/home/ben/spinifex/viperblock:

/home/ben/spinifex/volumes:
```

### Quick Installation

```bash
sudo ./config/install-spinifex-service.sh
```

### Manual Installation

If you prefer to install manually:

1. Copy the service file:
   ```bash
   sudo cp config/spinifex.service /etc/systemd/system/
   ```

2. Reload systemd:
   ```bash
   sudo systemctl daemon-reload
   ```

3. Enable the service:
   ```bash
   sudo systemctl enable spinifex.service
   ```

## Service Management

### Start the service
```bash
sudo systemctl start spinifex
```

### Stop the service
```bash
sudo systemctl stop spinifex
```

### Check service status
```bash
sudo systemctl status spinifex
```

### Restart the service
```bash
sudo systemctl restart spinifex
```

### View service logs
```bash
sudo journalctl -u spinifex -f
```

### Disable auto-start
```bash
sudo systemctl disable spinifex
```

## Service Details

The `spinifex.service` file:

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
sudo systemctl is-active spinifex
```

### View recent logs
```bash
sudo journalctl -u spinifex --since "1 hour ago"
```

### Check service configuration
```bash
sudo systemctl cat spinifex
```

### Verify binary exists
```bash
ls -la /opt/spinifex/bin/spx
```

### Test service commands manually
```bash
/opt/spinifex/bin/spx service nats start
/opt/spinifex/bin/spx service predastore start
/opt/spinifex/bin/spx service viperblock start
```

## Customization

If you need to modify the service configuration:

1. Edit the service file: `sudo nano /etc/systemd/system/spinifex.service`
2. Reload systemd: `sudo systemctl daemon-reload`
3. Restart the service: `sudo systemctl restart spinifex`

## Security Notes

- The service runs as root, which is necessary for system-level services
- Security hardening is enabled with `ProtectSystem=strict` and `NoNewPrivileges=true`
- Only specific paths are writable: `/opt/spinifex`, `/var/log`, `/var/run` 