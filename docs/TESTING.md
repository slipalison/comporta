# Testing

`go test ./...` needs nothing installed: the SQL tests use SQLite files and the Redis tests use an in-memory server ([miniredis](https://github.com/alicebob/miniredis)).

The tests in `internal/mcpsrv/integration_test.go` talk to real Oracle, ClickHouse and Redis servers. Each one runs only when its connection string is set, so CI skips them. Here is how to run them on your machine with Docker.

## 1. Start the databases

```bash
docker run -d --name ct-redis -p 127.0.0.1:16379:6379 redis:8-alpine

docker run -d --name ct-clickhouse -p 127.0.0.1:19000:9000 \
  -e CLICKHOUSE_USER=app -e CLICKHOUSE_PASSWORD=pass -e CLICKHOUSE_DB=shop \
  clickhouse/clickhouse-server:25.8-alpine

# Oracle needs about 2 GB of memory and a minute or two to start.
docker run -d --name ct-oracle -p 127.0.0.1:11521:1521 \
  -e ORACLE_PASSWORD=adminpass -e APP_USER=app -e APP_USER_PASSWORD=apppass \
  gvenzl/oracle-free:23-slim-faststart
docker logs -f ct-oracle   # wait for "DATABASE IS READY TO USE!"
```

## 2. Load the test data

```bash
docker exec ct-redis redis-cli MSET user:1 ana user:2 bia
docker exec ct-redis redis-cli HSET session:42 user 1 ttl 30

docker exec ct-clickhouse clickhouse-client -u app --password pass -d shop --multiquery -q "
  CREATE TABLE orders (id UInt32, customer String, total Float64, tags Array(String), note Nullable(String))
    ENGINE = MergeTree ORDER BY id;
  INSERT INTO orders VALUES (1, 'ana', 10.5, ['vip'], NULL), (2, 'bia', 20, [], 'x');"

docker exec -i ct-oracle sqlplus -s app/apppass@localhost/FREEPDB1 <<'SQL'
CREATE TABLE orders (id NUMBER PRIMARY KEY, customer VARCHAR2(50), total NUMBER(10,2), created DATE DEFAULT SYSDATE);
INSERT INTO orders (id, customer, total) VALUES (1, 'ana', 10.5);
INSERT INTO orders (id, customer, total) VALUES (2, 'bia', 20);
COMMIT;
-- A function that writes, to prove a "read" cannot call it.
CREATE OR REPLACE FUNCTION sneaky RETURN NUMBER IS BEGIN DELETE FROM orders; RETURN 1; END;
/
SQL
```

## 3. Run the tests

```bash
COMPORTA_TEST_ORACLE='oracle://app:apppass@127.0.0.1:11521/FREEPDB1' \
COMPORTA_TEST_CLICKHOUSE='clickhouse://app:pass@127.0.0.1:19000/shop' \
COMPORTA_TEST_REDIS='redis://127.0.0.1:16379/0' \
go test ./internal/mcpsrv/ -run Live -v
```

What they check, besides the happy path:

- **Oracle:** `SELECT sneaky() FROM dual` fails inside the database (`ORA-14551`), so a function that writes cannot run from `sql_query`; `q'[...]'` quotes; `dry_run` rolls back.
- **ClickHouse:** a query that tries `SETTINGS readonly = 0` is refused by the server; `dry_run` is refused before reaching it; arrays and `Nullable` columns come back as JSON.
- **Redis:** a write sent to `redis_query` is denied; `describe_schema` samples keys with `SCAN`.

Remove the containers afterwards with `docker rm -f ct-redis ct-clickhouse ct-oracle`.
