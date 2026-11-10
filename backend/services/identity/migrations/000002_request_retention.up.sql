CREATE TABLE api_requests (
 scope text NOT NULL,key_hash text NOT NULL,request_hash text NOT NULL,response bytea NOT NULL,
 expires_at timestamptz NOT NULL,PRIMARY KEY(scope,key_hash)
);
CREATE INDEX api_requests_expiry_idx ON api_requests(expires_at);
