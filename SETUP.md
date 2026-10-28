# First install on a dev store (Windows)

Do these in order. Steps 1 to 4 are one-time setup; steps 5 to 9 are what you repeat each session.

## 1. Install the tools

- **Go 1.24 or newer:** https://go.dev/dl/ — check with `go version`.
- **Docker Desktop:** https://www.docker.com/products/docker-desktop/ — runs Postgres and Redis.
- **cloudflared:** `winget install --id Cloudflare.cloudflared` — gives your laptop a public https URL Shopify can reach.

## 2. Create a development store

1. In the Partner Dashboard (partners.shopify.com), open **Stores** → **Add store** → **Create development store**. If Shopify sends you to the Dev Dashboard (dev.shopify.com), use **Dev stores** → **Create dev store** there.
2. Choose "Create a store to test and build", name it e.g. `feeddoc-test`, and create it.
3. Note its domain: `feeddoc-test.myshopify.com` (or whatever you picked).

## 3. Load the messy test products

1. In the dev store admin: **Products** → **Import** → choose `testdata\dev_store_products.csv` from this folder → **Upload and preview** → **Import products**.
2. You should see 6 products, plus 1 draft (the shawl). Each one has deliberate problems: promotional title, invalid barcode, zero price, missing description and image, "Refurbished" in the title while the condition is new, a reused SKU, and a missing brand.
3. In **Settings → Policies**, leave the refund policy empty for now. The app should flag that as an account-level risk.

## 4. Start the tunnel and create the app

1. In a terminal, start the tunnel and leave it running:
   ```powershell
   cloudflared tunnel --url http://localhost:8080
   ```
   Copy the `https://<random>.trycloudflare.com` URL it prints. This is your **APP_URL**. It changes every time you restart cloudflared, so you will update the app's URLs each time (step 4.4).
2. In the Partner Dashboard: **Apps** → **Create app** → **Create app manually**. Name it `Feed Doctor`.
3. On the app's **Client credentials** (or **Settings**) page, copy the **Client ID** and **Client secret**. Do not paste the secret into chat or commit it.
4. On **Configuration** (in the Dev Dashboard, this means creating a version):
   - **App URL:** `https://<random>.trycloudflare.com/`
   - **Allowed redirection URL(s):** `https://<random>.trycloudflare.com/auth/callback`
   - **Embed app in Shopify admin:** on
   - **Use legacy install flow:** on. The server runs the OAuth install itself.
   - **Scopes:** `read_products`
   - **Compliance webhooks** (customer data request, customer erasure, shop erasure): all three `https://<random>.trycloudflare.com/webhooks`
   - Save, or release the version.

## 5. Start Postgres (and optionally Redis)

**With Docker:** in the FeedDoc folder run `docker compose up -d`. The database is then on port 5433.

**Without Docker:** use the Postgres installed on Windows (port 5432). Create the app's user and database once, from any PowerShell window. It asks for the `postgres` password you chose when installing Postgres:
```powershell
& (Get-ChildItem "C:\Program Files\PostgreSQL\*\bin\psql.exe" | Select-Object -First 1).FullName -U postgres -c "CREATE USER feeddoctor PASSWORD 'feeddoctor';" -c "CREATE DATABASE feeddoctor OWNER feeddoctor;"
```
Redis is optional: when `REDIS_URL` is not set, the server keeps its rescan queue in memory, which is fine for testing on one machine.

## 6. Run the server

In a second terminal, in the FeedDoc folder:
```powershell
$env:SHOPIFY_API_KEY    = "<Client ID>"
$env:SHOPIFY_API_SECRET = "<Client secret>"
$env:APP_URL            = "https://<random>.trycloudflare.com"
$env:DATABASE_URL       = "postgres://feeddoctor:feeddoctor@localhost:5433/feeddoctor"   # 5432 without Docker
$env:REDIS_URL          = "redis://localhost:6379/0"                                 # leave out without Docker
go run ./cmd/feeddoctor-server
```
It should print `msg=listening addr=:8080`. Check `https://<random>.trycloudflare.com/healthz` in a browser; it should say `ok`.

## 7. Install the app on the dev store

1. In the Partner Dashboard, open the app → **Test your app** / **Select store** → choose `feeddoc-test`. Alternatively open
   `https://<random>.trycloudflare.com/auth?shop=feeddoc-test.myshopify.com`.
2. Approve the permissions. You land on the app inside the store admin.
3. The server log should show `msg=installed`, then within a minute `msg="full scan"` with a result like `opened=…`.

## 8. Look at the results

Refresh the app page in the admin. You should see a health score well below 100%, disapprovals for the planted problems, and links to each product.

## 9. Check that live rescans work

Edit the kurta's title to `Handloom Cotton Kurta` and save. Within about 5 seconds the log shows `msg="product scan"` with `fixed=2`, and after a refresh the promotional and all-caps title issues are gone.

## If something fails

Copy the server log lines around the error (they never contain the secret) and send them to Claude. The parts most likely to need adjusting against the real API are the bulk export query and the webhook registration.
