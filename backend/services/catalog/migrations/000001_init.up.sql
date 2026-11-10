CREATE TABLE baths(id integer PRIMARY KEY,name text NOT NULL,address text NOT NULL);
CREATE TABLE departments(id text PRIMARY KEY,bath_id integer NOT NULL REFERENCES baths(id),name text NOT NULL,mode text NOT NULL CHECK(mode IN ('session','sauna','info')),description text NOT NULL,active boolean NOT NULL DEFAULT true);
CREATE INDEX departments_bath_idx ON departments(bath_id) WHERE active;
