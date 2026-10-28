-- Applied on every start; every statement is idempotent.

CREATE TABLE IF NOT EXISTS shops (
    domain         text PRIMARY KEY,
    access_token   text NOT NULL,
    scope          text NOT NULL DEFAULT '',
    store_info     jsonb,
    live_products  int  NOT NULL DEFAULT 0,
    installed_at   timestamptz NOT NULL DEFAULT now(),
    uninstalled_at timestamptz,
    last_full_scan timestamptz
);

ALTER TABLE shops ADD COLUMN IF NOT EXISTS last_scan_error text NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS scans (
    id               bigserial PRIMARY KEY,
    shop             text NOT NULL REFERENCES shops(domain) ON DELETE CASCADE,
    kind             text NOT NULL,          -- full or products
    started_at       timestamptz NOT NULL,
    finished_at      timestamptz NOT NULL DEFAULT now(),
    products_checked int NOT NULL,
    opened           int NOT NULL,
    reopened         int NOT NULL,
    fixed            int NOT NULL,
    still_open       int NOT NULL,
    removed          int NOT NULL
);
CREATE INDEX IF NOT EXISTS scans_shop_time ON scans (shop, finished_at DESC);

-- One row per distinct problem. A row is never deleted when the problem is
-- fixed, so the admin can show new, fixed and recurring problems over time.
CREATE TABLE IF NOT EXISTS issues (
    shop           text NOT NULL REFERENCES shops(domain) ON DELETE CASCADE,
    fingerprint    text NOT NULL,
    rule_id        text NOT NULL,
    severity       text NOT NULL,
    product_id     text NOT NULL DEFAULT '',
    product_title  text NOT NULL DEFAULT '',
    variant_id     text NOT NULL DEFAULT '',
    field          text NOT NULL DEFAULT '',
    message        text NOT NULL,
    fix            text NOT NULL,
    auto_fixable   boolean NOT NULL DEFAULT false,
    status         text NOT NULL,            -- open or fixed
    first_seen     timestamptz NOT NULL,
    last_seen      timestamptz NOT NULL,
    fixed_at       timestamptz,
    times_reopened int NOT NULL DEFAULT 0,
    PRIMARY KEY (shop, fingerprint)
);
CREATE INDEX IF NOT EXISTS issues_shop_product ON issues (shop, product_id);
CREATE INDEX IF NOT EXISTS issues_shop_status ON issues (shop, status);
