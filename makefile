# Project paths
TAILWIND_BIN=./tools/tailwindcss
INPUT_CSS=./assets/css/input.css
OUTPUT_CSS=./static/css/output.css

# Determine platform
UNAME_S := $(shell uname -s)
UNAME_M := $(shell uname -m)

# Figure out which binary to download
ifeq ($(UNAME_S),Darwin)
  ifeq ($(UNAME_M),arm64)
    TAILWIND_URL=https://github.com/tailwindlabs/tailwindcss/releases/latest/download/tailwindcss-macos-arm64
  else
    TAILWIND_URL=https://github.com/tailwindlabs/tailwindcss/releases/latest/download/tailwindcss-macos-x64
  endif
endif

# Linux support (assuming x64 for now)
ifeq ($(UNAME_S),Linux)
  TAILWIND_URL=https://github.com/tailwindlabs/tailwindcss/releases/latest/download/tailwindcss-linux-x64
endif

# Install Tailwind CLI
.PHONY: tailwind-install
tailwind-install:
	@echo "🛠️  Downloading Tailwind CLI for $(UNAME_S)/$(UNAME_M)..."
	mkdir -p tools
	curl -sLo $(TAILWIND_BIN) $(TAILWIND_URL)
	chmod +x $(TAILWIND_BIN)
	@if [ "$(UNAME_S)" = "Darwin" ]; then \
		xattr -d com.apple.quarantine $(TAILWIND_BIN) || true; \
	fi
	@echo "✅ Tailwind CLI ready at $(TAILWIND_BIN)"

# Build Tailwind CSS
.PHONY: build-css
build-css: tailwind-install
	@echo "🧵 Compiling Tailwind CSS..."
	$(TAILWIND_BIN) -i $(INPUT_CSS) -o $(OUTPUT_CSS) --minify
	@echo "🎉 Done!"

# Watch Tailwind CSS in dev mode
.PHONY: watch-css
watch-css: tailwind-install
	@echo "👀 Watching Tailwind CSS..."
	$(TAILWIND_BIN) -i $(INPUT_CSS) -o $(OUTPUT_CSS) --watch

# Database migration commands
.PHONY: migrate
migrate:
	@echo "🗃️  Running DB migrations..."
	go run ./cmd/migrate

# Seed command currently runs idempotent migration seeds.
.PHONY: seed
seed:
	@echo "🌱 Applying seed data..."
	go run ./cmd/migrate

.PHONY: seed-demo
seed-demo:
	@echo "🧪 Applying demo seed data..."
	go run ./cmd/seed-demo

.PHONY: bootstrap-admin
bootstrap-admin:
	@if [ -z "$(EMAIL)" ]; then \
		echo "Usage: make bootstrap-admin EMAIL=user@example.com"; \
		exit 1; \
	fi
	@echo "🔐 Bootstrapping admin for $(EMAIL)..."
	go run ./cmd/bootstrap-admin -email "$(EMAIL)"

.PHONY: bootstrap-user
bootstrap-user:
	@if [ -z "$(EMAIL)" ] || [ -z "$(PASSWORD)" ]; then \
		echo "Usage: make bootstrap-user EMAIL=user@example.com PASSWORD=secret [NAME='Admin User'] [ADMIN=false]"; \
		exit 1; \
	fi
	@echo "👤 Bootstrapping user $(EMAIL)..."
	printf '%s\n' "$(PASSWORD)" | go run ./cmd/bootstrap-user -email "$(EMAIL)" -name "$(NAME)" -password-stdin -admin=$(if $(ADMIN),$(ADMIN),true)
