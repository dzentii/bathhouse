ALTER TABLE departments
 ADD COLUMN min_duration_minutes integer NOT NULL DEFAULT 60 CHECK(min_duration_minutes BETWEEN 1 AND 1440),
 ADD COLUMN duration_step_minutes integer NOT NULL DEFAULT 60 CHECK(duration_step_minutes BETWEEN 1 AND 1440),
 ADD COLUMN start_step_minutes integer NOT NULL DEFAULT 30 CHECK(start_step_minutes BETWEEN 1 AND 1440),
 ADD COLUMN buffer_minutes integer NOT NULL DEFAULT 30 CHECK(buffer_minutes BETWEEN 0 AND 240);
UPDATE departments SET buffer_minutes=0 WHERE mode='session';
