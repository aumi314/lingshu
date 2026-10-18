# 本地开发命令。

DB_CONTAINER := lingshu-postgres
DB_IMAGE     := postgres:17-alpine
DB_PORT      := 55432
DEV_DB_URL   := postgres://lingshu:lingshu_dev@127.0.0.1:$(DB_PORT)/lingshu?sslmode=disable
TEST_DB_URL  := postgres://lingshu:lingshu_dev@127.0.0.1:$(DB_PORT)/lingshu_test?sslmode=disable

.PHONY: db-up db-down dev test build

# 起本地 Postgres 容器（只给灵枢用，不碰本机其他数据库），并建好开发库和测试库
db-up:
	@docker start $(DB_CONTAINER) >/dev/null 2>&1 || docker run -d --name $(DB_CONTAINER) \
		-e POSTGRES_USER=lingshu -e POSTGRES_PASSWORD=lingshu_dev -e POSTGRES_DB=lingshu \
		-p 127.0.0.1:$(DB_PORT):5432 -v lingshu-pgdata:/var/lib/postgresql/data $(DB_IMAGE) >/dev/null
	@until docker exec $(DB_CONTAINER) pg_isready -h 127.0.0.1 -U lingshu >/dev/null 2>&1; do sleep 1; done
	@docker exec $(DB_CONTAINER) psql -U lingshu -tAc "SELECT 1 FROM pg_database WHERE datname='lingshu_test'" | grep -q 1 \
		|| docker exec $(DB_CONTAINER) createdb -U lingshu lingshu_test
	@echo "本地数据库已就绪: $(DEV_DB_URL)"

db-down:
	docker stop $(DB_CONTAINER)

dev:
	LINGSHU_DATABASE_URL='$(DEV_DB_URL)' go run ./cmd/lingshu serve

test:
	LINGSHU_TEST_DATABASE_URL='$(TEST_DB_URL)' go test -p 1 ./...

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o bin/lingshu ./cmd/lingshu
