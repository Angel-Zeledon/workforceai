.PHONY: up demo down logs test test-backend test-runtime test-e2e test-e2e-auth smoke reset \
        dev-backend dev-runtime dev-frontend dev-infra

# Accounts on: sign up / log in at http://localhost:3000.
up:
	docker compose up --build -d

# Open demo (no login, fixed demo organization): what smoke and e2e run against.
demo:
	docker compose -f docker-compose.yml -f docker-compose.demo.yml up --build -d

down:
	docker compose down

logs:
	docker compose logs -f --tail=100

# Unit tests de los tres servicios (no requiere el stack levantado).
test: test-backend test-runtime
	cd frontend && npm run build

test-backend:
	cd backend && go test ./...

test-runtime:
	cd agent-runtime && python -m pytest -q

# E2E Playwright (requiere `make demo`: demo abierta sin login).
test-e2e:
	cd e2e && npm install && npx playwright install chromium && npm test

# E2E de cuentas (requiere `make up`: login activado).
test-e2e-auth:
	cd e2e && npm install && npx playwright install chromium && E2E_AUTH=1 npx playwright test auth.spec.ts approvals-auth.spec.ts

# Smoke del escenario de $50,000 (requiere `make demo`).
smoke:
	bash scripts/smoke.sh

# Limpia datos de ejecución y reseed.
reset:
	curl -fsS -X POST http://localhost:8080/api/v1/demo/reset

# Desarrollo local por servicio (cada uno en su terminal).
dev-infra:
	docker compose up -d postgres redis

dev-backend: dev-infra
	cd backend && DATABASE_URL="postgres://workforce:workforce@localhost:5432/workforce?sslmode=disable" \
	  REDIS_URL="redis://localhost:6379" RUNTIME_URL="http://localhost:8000" PORT=8080 go run ./cmd/server

dev-runtime:
	cd agent-runtime && python -m uvicorn app.main:app --reload --port 8000

dev-frontend:
	cd frontend && NEXT_PUBLIC_API_URL=http://localhost:8080/api/v1 NEXT_PUBLIC_WS_URL=ws://localhost:8080/ws npm run dev
