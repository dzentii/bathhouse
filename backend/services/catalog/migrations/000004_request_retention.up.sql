CREATE TABLE api_requests (
 scope text NOT NULL,key_hash text NOT NULL,request_hash text NOT NULL,response bytea NOT NULL,
 expires_at timestamptz NOT NULL,PRIMARY KEY(scope,key_hash)
);
CREATE INDEX api_requests_expiry_idx ON api_requests(expires_at);
ALTER TABLE departments ADD COLUMN version bigint NOT NULL DEFAULT 1;
CREATE TABLE catalog_audit(id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,admin_id uuid NOT NULL,department_id text NOT NULL,before_value jsonb NOT NULL,after_value jsonb NOT NULL,created_at timestamptz NOT NULL DEFAULT clock_timestamp());
