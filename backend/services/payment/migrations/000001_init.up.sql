CREATE TABLE attempts (
 id uuid PRIMARY KEY,order_id uuid NOT NULL,idempotency_hash text NOT NULL,request_hash text NOT NULL,
 amount_kopecks bigint NOT NULL CHECK(amount_kopecks>0),currency text NOT NULL CHECK(currency='RUB'),outcome text NOT NULL CHECK(outcome IN ('success','declined')),
 status text NOT NULL CHECK(status IN ('pending','accepted','declined','rejected')),reason text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(order_id,idempotency_hash)
);
CREATE INDEX attempts_order_idx ON attempts(order_id,created_at);
CREATE TABLE inbox(id uuid PRIMARY KEY,processed_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE outbox(id uuid PRIMARY KEY,aggregate_id text NOT NULL,payload jsonb NOT NULL,created_at timestamptz NOT NULL DEFAULT clock_timestamp(),delivered_at timestamptz,attempts integer NOT NULL DEFAULT 0,next_attempt_at timestamptz NOT NULL DEFAULT now());
CREATE INDEX outbox_pending_idx ON outbox(next_attempt_at,created_at) WHERE delivered_at IS NULL;
