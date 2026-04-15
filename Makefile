.PHONY: build up down logs test lint fmt vet tidy test-integration clean

# ── Docker Compose ──────────────────────────────────────────────
up:
	docker-compose up --build

up-detached:
	docker-compose up --build -d

down:
	docker-compose down

logs:
	docker-compose logs -f

logs-replicas:
	docker-compose logs -f replica1 replica2 replica3 replica4

logs-gateway:
	docker-compose logs -f gateway

# ── Build (local, no Docker) ───────────────────────────────────
build:
	cd gateway && go build -o bin/gateway ./cmd/...
	cd replica && go build -o bin/replica ./cmd/...

# ── Test ────────────────────────────────────────────────────────
test:
	cd types && go test ./...
	cd replica && go test -race -count=1 ./...
	cd gateway && go test -race -count=1 ./...

test-verbose:
	cd replica && go test -race -count=1 -v ./...
	cd gateway && go test -race -count=1 -v ./...

test-coverage:
	cd replica && go test -race -coverprofile=coverage.out ./... && go tool cover -html=coverage.out -o coverage.html
	cd gateway && go test -race -coverprofile=coverage.out ./... && go tool cover -html=coverage.out -o coverage.html

test-integration:
	@echo "Integration tests require docker-compose up"
	cd replica && go test -race -tags=integration -count=1 -v ./...

# ── Code Quality ────────────────────────────────────────────────
fmt:
	cd types && go fmt ./...
	cd replica && go fmt ./...
	cd gateway && go fmt ./...

vet:
	cd types && go vet ./...
	cd replica && go vet ./...
	cd gateway && go vet ./...

lint:
	cd replica && golangci-lint run ./...
	cd gateway && golangci-lint run ./...

tidy:
	cd types && go mod tidy
	cd replica && go mod tidy
	cd gateway && go mod tidy

# ── Utility ─────────────────────────────────────────────────────
clean:
	cd gateway && rm -rf bin/ tmp/ coverage.out coverage.html
	cd replica && rm -rf bin/ tmp/ coverage.out coverage.html

status:
	@echo "=== Replica 1 ===" && curl -s http://localhost:9001/status | python3 -m json.tool 2>/dev/null || echo "DOWN"
	@echo "=== Replica 2 ===" && curl -s http://localhost:9002/status | python3 -m json.tool 2>/dev/null || echo "DOWN"
	@echo "=== Replica 3 ===" && curl -s http://localhost:9003/status | python3 -m json.tool 2>/dev/null || echo "DOWN"
	@echo "=== Replica 4 ===" && curl -s http://localhost:9004/status | python3 -m json.tool 2>/dev/null || echo "DOWN"

health:
	@curl -sf http://localhost:8080/health && echo " Gateway OK" || echo "Gateway DOWN"
	@curl -sf http://localhost:9001/health && echo " Replica1 OK" || echo "Replica1 DOWN"
	@curl -sf http://localhost:9002/health && echo " Replica2 OK" || echo "Replica2 DOWN"
	@curl -sf http://localhost:9003/health && echo " Replica3 OK" || echo "Replica3 DOWN"
	@curl -sf http://localhost:9004/health && echo " Replica4 OK" || echo "Replica4 DOWN"

# ── Kill a replica for testing ──────────────────────────────────
kill-leader:
	@LEADER_ADDR=$$(curl -s http://localhost:8080/leader | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('leaderAddr') or d.get('leader',''))") && \
	LEADER=$${LEADER_ADDR%%:*} && \
	echo "Killing leader at: $$LEADER_ADDR (container: $$LEADER)" && \
	docker-compose stop $$LEADER

restart-all:
	docker-compose restart replica1 replica2 replica3 replica4

# ── Partition Testing (Bonus B1) ────────────────────────────────
partition-isolate:
	@bash scripts/partition.sh isolate $(NODE)

partition-heal:
	@bash scripts/partition.sh heal $(NODE)

partition-heal-all:
	@bash scripts/partition.sh heal-all

partition-status:
	@bash scripts/partition.sh status

chaos:
	@bash scripts/partition.sh chaos $(or $(DURATION),30)
