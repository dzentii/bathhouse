DROP INDEX employee_audit_actor_page_idx;
DROP INDEX employee_audit_page_idx;
DROP INDEX employee_audit_employee_page_idx;
ALTER TABLE employee_audit DROP COLUMN before_value,DROP COLUMN after_value;
