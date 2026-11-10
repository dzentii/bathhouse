DROP TABLE employee_audit;
DROP INDEX sessions_admin_idx;
DROP INDEX sessions_id_idx;
ALTER TABLE sessions DROP COLUMN user_agent,DROP COLUMN id;
DROP INDEX admins_username_lower_idx;
ALTER TABLE admins DROP COLUMN role,DROP COLUMN active,DROP COLUMN display_name,DROP COLUMN version;
