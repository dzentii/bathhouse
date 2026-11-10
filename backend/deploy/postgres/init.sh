#!/bin/sh
set -eu
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
  --set=catalog_pw="$CATALOG_DB_PASSWORD" --set=booking_pw="$BOOKING_DB_PASSWORD" \
  --set=payment_pw="$PAYMENT_DB_PASSWORD" --set=identity_pw="$IDENTITY_DB_PASSWORD" <<'SQL'
CREATE ROLE catalog LOGIN PASSWORD :'catalog_pw';
CREATE ROLE booking LOGIN PASSWORD :'booking_pw';
CREATE ROLE payment LOGIN PASSWORD :'payment_pw';
CREATE ROLE identity LOGIN PASSWORD :'identity_pw';
CREATE DATABASE catalog OWNER catalog;
CREATE DATABASE booking OWNER booking;
CREATE DATABASE payment OWNER payment;
CREATE DATABASE identity OWNER identity;
REVOKE CONNECT ON DATABASE catalog FROM PUBLIC;
REVOKE CONNECT ON DATABASE booking FROM PUBLIC;
REVOKE CONNECT ON DATABASE payment FROM PUBLIC;
REVOKE CONNECT ON DATABASE identity FROM PUBLIC;
GRANT CONNECT ON DATABASE catalog TO catalog;
GRANT CONNECT ON DATABASE booking TO booking;
GRANT CONNECT ON DATABASE payment TO payment;
GRANT CONNECT ON DATABASE identity TO identity;
SQL
