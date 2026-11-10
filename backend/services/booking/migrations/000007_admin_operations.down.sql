DROP INDEX resources_department_page_idx;
DROP INDEX orders_resource_search_idx;
DROP INDEX orders_contact_phone_search_idx;
ALTER TABLE departments DROP CONSTRAINT departments_sales_pause_check;
ALTER TABLE departments DROP COLUMN sales_pause_reason,DROP COLUMN sales_paused_until,DROP COLUMN online_sales_enabled;
