DROP TABLE outbox,inbox,catalog_changes;
ALTER TABLE departments DROP COLUMN sync_status,DROP COLUMN sync_error,DROP COLUMN booking_setup;
ALTER TABLE baths DROP COLUMN description,DROP COLUMN active,DROP COLUMN version;
ALTER TABLE baths ALTER COLUMN id DROP DEFAULT;
DROP SEQUENCE bath_id_seq;
