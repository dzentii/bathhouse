ALTER TABLE departments ADD COLUMN archived boolean NOT NULL DEFAULT false;
ALTER TABLE departments ADD COLUMN catalog_version bigint NOT NULL DEFAULT 1;
ALTER TABLE departments ADD COLUMN policy_version bigint NOT NULL DEFAULT 1;
ALTER TABLE departments ADD COLUMN scheme_version bigint NOT NULL DEFAULT 1;
ALTER TABLE resources ADD COLUMN active boolean NOT NULL DEFAULT true;
ALTER TABLE resources ADD COLUMN version bigint NOT NULL DEFAULT 1;
ALTER TABLE orders ADD COLUMN version bigint NOT NULL DEFAULT 1;
CREATE TABLE order_history(id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,order_id uuid NOT NULL REFERENCES orders(id),action text NOT NULL,actor_id uuid,details jsonb NOT NULL DEFAULT '{}',created_at timestamptz NOT NULL DEFAULT clock_timestamp());
CREATE INDEX order_history_order_idx ON order_history(order_id,created_at DESC,id);
CREATE INDEX outbox_order_history_idx ON outbox(aggregate_id,created_at DESC);

INSERT INTO order_history(order_id,action,details,created_at) SELECT id,'order.created',jsonb_build_object('department_id',department_id,'amount_kopecks',amount_kopecks),created_at FROM orders;
