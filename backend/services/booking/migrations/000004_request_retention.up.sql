CREATE TABLE api_requests (
 scope text NOT NULL,key_hash text NOT NULL,request_hash text NOT NULL,response bytea NOT NULL,
 expires_at timestamptz NOT NULL,PRIMARY KEY(scope,key_hash)
);
CREATE INDEX api_requests_expiry_idx ON api_requests(expires_at);
ALTER TABLE orders ADD COLUMN access_expires_at timestamptz;
ALTER TABLE orders ADD COLUMN contact_purge_at timestamptz;
ALTER TABLE orders ADD COLUMN contacts_purged_at timestamptz;
UPDATE orders SET access_expires_at=CASE WHEN status='confirmed' THEN ends_at+interval '30 days' ELSE created_at+interval '24 hours' END;
UPDATE orders SET contact_purge_at=access_expires_at;
ALTER TABLE orders ALTER COLUMN access_expires_at SET NOT NULL;
ALTER TABLE orders ALTER COLUMN access_expires_at SET DEFAULT (clock_timestamp()+interval '24 hours');
ALTER TABLE orders ALTER COLUMN contact_purge_at SET NOT NULL;
ALTER TABLE orders ALTER COLUMN contact_purge_at SET DEFAULT (clock_timestamp()+interval '24 hours');
CREATE INDEX orders_contact_purge_idx ON orders(contact_purge_at) WHERE contacts_purged_at IS NULL;
