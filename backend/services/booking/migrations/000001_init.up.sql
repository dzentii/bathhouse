CREATE EXTENSION IF NOT EXISTS btree_gist;
CREATE TABLE departments (
 id text PRIMARY KEY, bath_id integer NOT NULL, bath_name text NOT NULL, bath_address text NOT NULL, name text NOT NULL,
 mode text NOT NULL CHECK(mode IN ('session','sauna')),
 base_guests integer NOT NULL CHECK(base_guests>0), max_guests integer NOT NULL CHECK(max_guests>=base_guests),
 schedule jsonb NOT NULL, tariffs jsonb NOT NULL,
 schedule_version bigint NOT NULL DEFAULT 1, tariff_version bigint NOT NULL DEFAULT 1
);
CREATE TABLE resources(id text PRIMARY KEY,department_id text NOT NULL REFERENCES departments(id),label text NOT NULL,x integer NOT NULL,y integer NOT NULL,UNIQUE(department_id,label));
CREATE TABLE orders (
 id uuid PRIMARY KEY,department_id text NOT NULL REFERENCES departments(id),
 status text NOT NULL CHECK(status IN ('draft','held','confirmed','released','expired')),
 access_hash text NOT NULL, idempotency_hash text UNIQUE NOT NULL, request_hash text NOT NULL,
 selection jsonb NOT NULL,buyer jsonb NOT NULL,quote jsonb NOT NULL,
 amount_kopecks bigint NOT NULL CHECK(amount_kopecks>0),
 starts_at timestamptz NOT NULL,ends_at timestamptz NOT NULL CHECK(ends_at>starts_at),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),held_at timestamptz,expires_at timestamptz,confirmed_at timestamptz,
 ticket_hash text UNIQUE,ticket_expires_at timestamptz,
 CHECK(status<>'held' OR (held_at IS NOT NULL AND expires_at>held_at)),
 CHECK(status<>'confirmed' OR (confirmed_at IS NOT NULL AND ticket_hash IS NOT NULL AND ticket_expires_at IS NOT NULL))
);
CREATE INDEX orders_department_time_idx ON orders(department_id,starts_at);
CREATE INDEX orders_expiry_idx ON orders(expires_at) WHERE status='held';
CREATE INDEX orders_created_idx ON orders(created_at DESC,id);
CREATE TABLE allocations (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 order_id uuid NOT NULL REFERENCES orders(id),resource_id text NOT NULL REFERENCES resources(id),
 span tstzrange NOT NULL CHECK(NOT isempty(span) AND lower(span) IS NOT NULL AND upper(span) IS NOT NULL),
 active boolean NOT NULL DEFAULT true,UNIQUE(order_id,resource_id),
 EXCLUDE USING gist(resource_id WITH =,span WITH &&) WHERE(active)
);
CREATE INDEX allocations_order_idx ON allocations(order_id);
CREATE TABLE resource_blocks (
 id uuid PRIMARY KEY,department_id text NOT NULL REFERENCES departments(id),resource_ids text[] NOT NULL CHECK(cardinality(resource_ids)>0),
 starts_at timestamptz NOT NULL,ends_at timestamptz NOT NULL CHECK(ends_at>starts_at),reason text NOT NULL,version bigint NOT NULL DEFAULT 1,
 active boolean NOT NULL DEFAULT true,created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX blocks_department_time_idx ON resource_blocks(department_id,starts_at,ends_at) WHERE active;
CREATE TABLE payment_decisions(payment_id uuid PRIMARY KEY,order_id uuid NOT NULL REFERENCES orders(id),status text NOT NULL,reason text NOT NULL,created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE audit(id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,admin_id uuid NOT NULL,action text NOT NULL,entity_id text NOT NULL,before_value jsonb,after_value jsonb,created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE inbox(id uuid PRIMARY KEY,processed_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE outbox(id uuid PRIMARY KEY,aggregate_id text NOT NULL,payload jsonb NOT NULL,created_at timestamptz NOT NULL DEFAULT clock_timestamp(),delivered_at timestamptz,attempts integer NOT NULL DEFAULT 0,next_attempt_at timestamptz NOT NULL DEFAULT now());
CREATE INDEX outbox_pending_idx ON outbox(next_attempt_at,created_at) WHERE delivered_at IS NULL;
