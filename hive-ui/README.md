# Hive UI

Full-stack application for managing Hive AWS compatible products (EC2, S3)

## Setup

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

   Setup and run hive server. See [https://github.com/mulgadc/hive/blob/main/INSTALL.md](https://github.com/mulgadc/hive/blob/main/INSTALL.md) for installation documentation to setup a Hive cluster or single node for development purposes.

   Go to `https://localhost:9999` and `https://localhost:8443` and accept the certificates

   TODO: Tutorial on creating certificate authority and accepting generated certs as trusted.

5. **Generate Certs**
   ```sh
   openssl req -x509 -out certs/server.crt -keyout certs/server.key \
   -newkey rsa:2048 -nodes -sha256 \
   -subj '/CN=localhost' -extensions EXT -config <( \
      printf "[dn]\nCN=localhost\n[req]\ndistinguished_name = dn\n[EXT]\nsubjectAltName=DNS:localhost\nkeyUsage=digitalSignature\nextendedKeyUsage=serverAuth")
   ```

6. **Launch Server**

   For production, build the static assets and launch the `go` webservice for serving traffic:

   ```sh
   make run
   ```

   For development use:

   ```sh
   cd frontend
   pnpm dev
   ```

7. **Visit WebUI**

   View [https://localhost:3000](https://localhost:3000/) in your browser to continue.
