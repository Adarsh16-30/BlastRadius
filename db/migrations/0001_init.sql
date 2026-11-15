CREATE TABLE orders (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  idempotency_key text NOT NULL UNIQUE,
  customer_id     text NOT NULL,
  amount_cents    bigint NOT NULL CHECK (amount_cents > 0),
  created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX orders_created_at_idx ON orders (created_at DESC);

CREATE TABLE seq_log (
  writer_id  text NOT NULL,
  seq        bigint NOT NULL,
  written_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (writer_id, seq)
);
