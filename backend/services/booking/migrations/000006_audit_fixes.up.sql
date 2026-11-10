ALTER TABLE api_requests ADD COLUMN order_id uuid REFERENCES orders(id);
ALTER TABLE api_requests ADD COLUMN sensitive_until timestamptz;
UPDATE api_requests r SET order_id=o.id,sensitive_until=o.contact_purge_at,expires_at=LEAST(r.expires_at,o.contact_purge_at)
FROM orders o WHERE r.scope='order:create' AND r.key_hash=o.idempotency_hash;
UPDATE api_requests r SET order_id=o.id,sensitive_until=o.contact_purge_at,expires_at=LEAST(r.expires_at,o.contact_purge_at)
FROM orders o WHERE r.scope LIKE 'order:%:%' AND o.id=CASE WHEN split_part(r.scope,':',2) ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN split_part(r.scope,':',2)::uuid END;
DELETE FROM api_requests WHERE sensitive_until<=clock_timestamp();
CREATE INDEX api_requests_order_idx ON api_requests(order_id) WHERE order_id IS NOT NULL;
ALTER TABLE orders ADD COLUMN hold_client_key text NOT NULL DEFAULT '';
CREATE INDEX orders_hold_client_idx ON orders(hold_client_key,expires_at) WHERE status='held';
ALTER TABLE order_history ADD COLUMN source_event_id uuid UNIQUE;
INSERT INTO order_history(order_id,action,details,created_at,source_event_id)
SELECT o.id,b.payload->>'type',b.payload->'data',b.created_at,b.id FROM outbox b JOIN orders o ON b.aggregate_id=o.id::text
WHERE b.payload->>'type' IN ('order.held','order.released','order.expired','order.confirmed') ON CONFLICT(source_event_id) DO NOTHING;
CREATE INDEX orders_future_active_idx ON orders(department_id,ends_at) WHERE status IN ('held','confirmed');
CREATE INDEX audit_department_history_idx ON audit(entity_id,created_at DESC,id);
CREATE INDEX blocks_history_page_idx ON resource_blocks(department_id,starts_at DESC,id DESC);
CREATE INDEX orders_contacts_receipts_idx ON orders(contact_purge_at,id);
CREATE INDEX outbox_delivered_retention_idx ON outbox(delivered_at) WHERE delivered_at IS NOT NULL;
CREATE INDEX inbox_retention_idx ON inbox(processed_at);
