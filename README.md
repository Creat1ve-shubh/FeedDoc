# feeddoctor

Finds the product-data problems that get Shopify listings disapproved in Google Merchant Center, before Google does. It is a Go rules engine, a CLI, and a Shopify app server that scans a store on install, rescans products as webhooks say they change, and keeps a history of every problem: new, fixed, and recurring.

## Run it

```sh
go test ./...
go build -o bin/feeddoctor ./cmd/feeddoctor

./bin/feeddoctor scan -f testdata/messy_store.json            # full report
./bin/feeddoctor scan -f testdata/messy_store.json -min disapproval
./bin/feeddoctor scan -f testdata/clean_store.json -format json
./bin/feeddoctor rules                                         # list all checks
./bin/feeddoctor scan -f export.jsonl -store store.json        # a real Shopify bulk export
```

Exit code is `1` when anything would be disapproved, so the scan can gate a CI job or a nightly cron.

## What it checks

| Rule | Severity | Catches |
|---|---|---|
| `store.policies` | disapproval | No refund policy or contact details (account suspension risk) |
| `link.unpublished` | disapproval | Product not published to the Online Store |
| `title.missing` / `title.too_long` | disapproval / warning | Empty titles, titles over 150 characters |
| `title.promotional` | disapproval / warning | "Free shipping", "% off", "best price"; all-caps titles |
| `description.missing` | disapproval / warning | Empty, too thin, or over 5,000 characters |
| `image.missing` / `image.too_small` / `image.placeholder` | disapproval | No image, under 100 px (250 px for apparel), "no-image" placeholders |
| `price.invalid` / `price.compare_at` | disapproval / warning | Zero or blank prices; compare-at not above price |
| `gtin.invalid` | disapproval | Bad length, failed GS1 check digit, restricted 2/02/04 prefixes, placeholders |
| `gtin.missing` | warning / opportunity | Branded products without GTINs; own-brand products that should send `identifier_exists = no` |
| `brand.missing` | warning | No vendor |
| `category.missing` | opportunity | No Google product category |
| `apparel.attributes` | disapproval | Apparel missing gender, age group, color; clothing and shoes missing size |
| `availability.oversell` | warning | Zero stock but still selling, sent as in stock |
| `condition.mismatch` | disapproval | "Refurbished" or "used" in the title with condition left as new |
| `shipping.weight` | opportunity | Variants with no weight |
| `id.duplicate` | warning | SKUs or barcodes reused across variants |

Draft and archived products are skipped, because they are never sent to Google.

## Free web checker

`web/` is a static page that runs the same rules in the browser, with no Shopify app install. A merchant uploads the product CSV from Shopify's Export button (or types a store address) and gets the report, plus a fixed CSV to import back with "Overwrite products with matching handles". The fixed file holds only the changed products and columns, so images and inventory are left alone.

It fixes what needs no judgment: promotional and all-caps titles, titles over 150 characters, compare-at prices that are not a discount, used or refurbished items sold as new, missing brands (from the store name) and `identifier_exists` for own-brand products. Everything else is reported with a fix to make by hand. Rules the input cannot support, such as store policies in a CSV or barcodes on a store scan, are listed as not checked instead of passing.

```sh
web/build.sh                              # builds web/feeddoctor.wasm and copies the loader and example CSV
cd web && python3 -m http.server 8080     # open http://localhost:8080
```

To deploy on Cloudflare Pages, use `web/build.sh` as the build command and `web` as the output directory. Store-address scans need the worker in `web/proxy` (`cd web/proxy && npx wrangler deploy`), because browsers cannot read another site's `/products.json`. Put its URL in the `feeddoctor-proxy` meta tag in `web/index.html`; while that is empty, the store tab is switched off.

## Layout

```
cmd/feeddoctor         CLI
cmd/feeddoctor-server  Shopify app: install flow, webhooks, embedded admin, scan worker
cmd/feeddoctor-wasm    The checker compiled to WebAssembly for web/
internal/catalog       Store and product types, shaped like Shopify's Admin GraphQL output
internal/rules         The rules, the GTIN validator and the engine
internal/report        Health score, counts and text/JSON rendering
internal/shopify       OAuth, HMAC and session-token checks, Admin GraphQL client, bulk export parser
internal/store         Postgres: shops, scans, and issues with their history
internal/queue         Redis: per-shop sets of changed products, and full-scan requests
internal/scanner       Full and per-product scans, and the worker that runs them
internal/server        HTTP routes
internal/shopifycsv    Shopify product CSV export: parse, and write a fix file for import
internal/autofix       Fixes that need no judgment, applied to the CSV
internal/storefront    Catalog from a store's public pages (products.json, policies, contact)
internal/webscan       What the web checker calls: scan CSV or store, build the fixed file
web                    The web checker page and the store-fetch worker
testdata               Messy and clean stores, and a Shopify bulk export sample
```

## How the app works

1. **Install.** `/auth` sends the merchant to Shopify to approve `read_products` and `read_legal_policies` (for the refund and shipping policy checks). If the app later needs more scopes, opening it sends the merchant through approval again. `/auth/callback` checks the HMAC and the state cookie, swaps the code for an offline token, registers the product and uninstall webhooks, and queues a full scan.
2. **Full scan.** The worker reads the store's policies and contact details, runs a GraphQL bulk operation to export every product with its variants and images, runs every rule, and diffs the result against stored issues. Every shop gets a full scan again each 24 hours.
3. **Webhook rescans.** `products/create`, `update` and `delete` add the product id to a Redis set for the shop. Repeated updates to one product collapse into one entry. The worker refetches up to 250 changed products in one query, reruns the per-product rules, and only touches those products' issues. Catalog-wide rules (duplicate SKUs, store policies) wait for the next full scan.
4. **History.** An issue is keyed by rule, severity, product, variant and field. A scan marks it new, still open, fixed, or recurring when it was fixed and came back. Issues of deleted or drafted products are removed, not counted as fixed.
5. **Admin.** Shopify opens `/` inside the admin. A stopgap page loads `/api/summary` and `/api/issues` with an App Bridge session token. The Next.js admin will replace it.
6. **Uninstall and privacy.** `app/uninstalled` drops the token and keeps history for a reinstall. `shop/redact` deletes everything. The app stores no customer data, so the customer topics just return 200.

## Run the app locally

```sh
docker compose up -d                       # Postgres and Redis
cp .env.example .env                       # fill in the Partner Dashboard values
set -a; . ./.env; set +a                   # Git Bash or WSL; in PowerShell set the variables by hand
go run ./cmd/feeddoctor-server
```

Shopify has to reach the server over https, so point a tunnel at port 8080 (for example `cloudflared tunnel --url http://localhost:8080`) and use its URL as `APP_URL` and in `shopify.app.toml`. Then install the app on the dev store from the Partner Dashboard.

## Tests

The rules and Shopify tests need nothing. The store, queue, scanner and server tests run against real Postgres and Redis when these are set, and skip otherwise:

```sh
export FEEDDOCTOR_TEST_DATABASE_URL=postgres://feeddoctor:feeddoctor@localhost:5433/feeddoctor
export FEEDDOCTOR_TEST_REDIS_URL=redis://localhost:6379/1
go test -p 1 ./...                         # -p 1: the database tests share one database
```

## Adding a rule

Add a function with the signature `func(*catalog.Store, *catalog.Product) []Issue` to `internal/rules/product_rules.go`, register it in `All()`, plant a matching problem in `testdata/messy_store.json` and add it to `TestMessyStoreCatchesEveryPlantedProblem`.

## CI and images

`.github/workflows/ci.yml` runs gofmt, `go vet` and every test against Postgres and Redis service containers on each push and pull request. On `main` it then builds the `Dockerfile` and pushes the image to Docker Hub as `:latest` and `:sha-<commit>`. That step needs the repository secrets `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN`; `DOCKERHUB_REPO` (variable or secret) picks the image name and defaults to `<username>/feeddoctor`. Without the secrets the push is skipped. If the secret `RENDER_DEPLOY_HOOK` is set, the job then calls it so Render deploys the new image.

The image runs `feeddoctor-server` and is configured entirely by the environment variables in `.env.example`. Without `REDIS_URL` it uses an in-memory queue.

## Next

1. Install on a dev store and run a full scan against real products.
2. Next.js admin in `web/`: issues per product, filters by severity and status, a trend of health score over scans.
3. Auto-fix for the issues marked auto-fixable, behind the paid plan. This needs `write_products`.
4. Billing through the Shopify Billing API: free scan, paid fixes.
