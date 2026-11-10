ALTER TABLE employee_audit ADD COLUMN before_value jsonb;
ALTER TABLE employee_audit ADD COLUMN after_value jsonb;
CREATE INDEX employee_audit_employee_page_idx ON employee_audit(employee_id,created_at DESC,id);
CREATE INDEX employee_audit_page_idx ON employee_audit(created_at DESC,id);
CREATE INDEX employee_audit_actor_page_idx ON employee_audit(actor_id,created_at DESC,id);
