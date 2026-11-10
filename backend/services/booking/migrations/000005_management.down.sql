DROP INDEX outbox_order_history_idx;
DROP TABLE order_history;
ALTER TABLE orders DROP COLUMN version;
ALTER TABLE resources DROP COLUMN active,DROP COLUMN version;
ALTER TABLE departments DROP COLUMN archived,DROP COLUMN catalog_version,DROP COLUMN policy_version,DROP COLUMN scheme_version;
