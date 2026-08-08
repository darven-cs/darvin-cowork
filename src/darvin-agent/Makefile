# 不允许 agents/ 内部 import 任何"能力"包(llm/tools/skills/mcp/acp/gateway)
.PHONY: lint-agents-boundaries
lint-agents-boundaries:
	@echo ">>> Checking agents/ boundary violations..."
	@bad=0; \
	for pkg in $$(go list ./internal/agents/... 2>/dev/null); do \
		illegal=$$(go list -deps $$pkg 2>/dev/null | \
			grep -E 'darvin-cowork/backend/internal/(llm|tools|skills|mcp|acp|gateway|commands|memory|cron)' || true); \
		if [ -n "$$illegal" ]; then \
			echo "❌ $$pkg imports: $$illegal"; \
			bad=1; \
		fi; \
	done; \
	if [ $$bad -eq 0 ]; then \
		echo "✅ agents/ does not depend on capability packages"; \
	else \
		echo "❌ Violations found; agents/ must stay framework-only"; \
		exit 1; \
	fi

# Phase E toolchain targets
.PHONY: fmt fmt-check vet lint lint-comments check

fmt:
	gofmt -s -w .
	goimports -w -local darvin-cowork/ .

fmt-check:
	gofmt -l .
	goimports -l .

vet:
	go vet ./...

lint:
	golangci-lint run ./... || true

lint-comments:
	staticcheck -checks 'ST10*' ./...

check: fmt-check vet lint-comments lint
	bash scripts/check-agent-readability.sh
