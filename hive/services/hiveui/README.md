# Hive UI

Full-stack application for managing Hive AWS compatible products (EC2, S3)

## Development Setup

Note: This is not needed for actually building hive-ui and running it. We commit the assets so that when you build hive, it will include hive-ui without you needing to install node and build frontend from source.

1. **Install Node.js using nvm**

   This project uses Node.js 24.12.0 (specified in `.nvmrc`).

   ```sh
   cd frontend
   # Install nvm if you don't have it
   curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.3/install.sh | bash

   # Restart source nvm
   \. "$HOME/.nvm/nvm.sh"

   # Install and use the project's Node.js version
   nvm install
   nvm use
   ```

   The `.nvmrc` file in the project root ensures you use the correct Node.js version. Run `nvm use` whenever you enter this directory.

2. **Enable Corepack**
   ```sh
   corepack enable
   ```

3. **Install Dependencies**
   ```sh
   pnpm install
   ```

4. **Accept Certs In Browser**

   If you have added the CA to your machine you do not need to do this. But if you are sshd into a hive machine and want to view the ui, simply go to `https://localhost:9999` and `https://localhost:8443` and accept the certificates.

5. **Launch Server**

   For production, build the static assets and launch the `go` webservice for serving traffic:

   ```sh
   make run
   ```

   For development use:

   ```sh
   cd frontend
   pnpm dev
   ```

6. **Visit WebUI**

   View [https://localhost:3000](https://localhost:3000) in your browser to continue.
