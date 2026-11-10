ALTER TABLE departments ADD COLUMN online_sales_enabled boolean NOT NULL DEFAULT true;
ALTER TABLE departments ADD COLUMN sales_paused_until timestamptz;
ALTER TABLE departments ADD COLUMN sales_pause_reason text NOT NULL DEFAULT '';
ALTER TABLE departments ADD CONSTRAINT departments_sales_pause_check CHECK (
 (online_sales_enabled AND sales_paused_until IS NULL AND sales_pause_reason='') OR
 (NOT online_sales_enabled AND length(btrim(sales_pause_reason)) BETWEEN 1 AND 500));
CREATE INDEX orders_contact_phone_search_idx ON orders((buyer->>'phone'),created_at DESC,id DESC) WHERE contacts_purged_at IS NULL;
CREATE INDEX orders_resource_search_idx ON orders USING gin((selection->'resource_ids'));
CREATE INDEX resources_department_page_idx ON resources(department_id,id);
