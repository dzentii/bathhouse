CREATE INDEX outbox_delivered_retention_idx ON outbox(delivered_at) WHERE delivered_at IS NOT NULL;
CREATE INDEX inbox_retention_idx ON inbox(processed_at);
