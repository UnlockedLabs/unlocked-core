# pipefail so a failed `docker compose build` is not masked by the `tee` that
# keeps its output in logs/ — a silently swallowed tutor build was exactly how
# `make dev` used to come up without the tutor and say nothing useful.
SHELL := /bin/bash
.SHELLFLAGS := -o pipefail -c

DOCKER_COMPOSE=docker-compose.yml
TUTOR_BUILD_LOG=logs/tutor-build.log
KOLIBRI_COMPOSE=config/docker-compose.kolibri.yml
CANVAS_COMPOSE=config/docker-compose.canvas.yml
MIGRATE_MAIN=./backend/migrations -dir backend/migrations
BUILD_RECREATE=--build --force-recreate
# Set TUTOR_DIR in .env (or the environment) if your tutor checkout lives
# somewhere other than a sibling ../ai/ directory. Docker Compose reads the
# same variable from .env for the tutor build context, so the two never drift.
DEFAULT_AI_DIR=../ai/unlocked-hiset-ai
# Canvas admin created on first `make canvas`. Override in the environment if
# you want different credentials. These are also what you log into Canvas with
# at localhost:3001 to mint the API token.
CANVAS_ADMIN_EMAIL ?= superadmin@unlocked.local
CANVAS_ADMIN_PASSWORD ?= ChangeMe!
CANVAS_ACCOUNT_NAME ?= UnlockEd Dev

# tutor-service only builds from the sibling ../ai/unlocked-hiset-ai checkout when
# it's present; otherwise its published image is pulled instead. Building it
# separately (not as part of one big `docker compose up --build`) matters: a
# `docker compose up --build` builds every service before starting any of
# them, so a broken tutor-service build previously aborted the whole command
# and took the ENTIRE stack down with it — falling back to the published image
# on a build failure keeps that from happening. $(1) is extra `docker compose`
# flags (e.g. `-f some-file.yml`).
define run_dev_compose
	@set -e; \
	if [ ! -f .env ]; then \
		echo; \
		echo "=============================================================="; \
		echo " NO .env FILE FOUND in $$(pwd)."; \
		echo " The stack cannot start without it. Create it with:"; \
		echo; \
		echo "     cp .env.example .env"; \
		echo; \
		echo " then re-run this target."; \
		echo "=============================================================="; \
		echo; \
		exit 1; \
	fi; \
	export GIT_COMMIT=$$(git rev-parse --short HEAD 2>/dev/null || echo unknown); \
	export GIT_BRANCH=$$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo unknown); \
	AI_DIR=$${TUTOR_DIR:-$$(sed -n 's/^TUTOR_DIR=//p' .env | tail -1)}; \
	AI_DIR=$${AI_DIR:-$(DEFAULT_AI_DIR)}; \
	TUTOR_READY=0; TUTOR_WHY=; \
	if [ -d "$$AI_DIR" ]; then \
		export TUTOR_GIT_COMMIT=$$(git -C "$$AI_DIR" rev-parse --short HEAD 2>/dev/null || echo unknown); \
		export TUTOR_GIT_BRANCH=$$(git -C "$$AI_DIR" rev-parse --abbrev-ref HEAD 2>/dev/null || echo unknown); \
		echo "Building tutor-service from $$AI_DIR @ $$TUTOR_GIT_BRANCH ($$TUTOR_GIT_COMMIT)"; \
		mkdir -p $(dir $(TUTOR_BUILD_LOG)); \
		if docker compose $(1) build tutor-service 2>&1 | tee $(TUTOR_BUILD_LOG); then \
			TUTOR_READY=1; \
		else \
			TUTOR_WHY="the build from $$AI_DIR failed — full output in $(TUTOR_BUILD_LOG)"; \
			echo "tutor-service failed to build — trying to pull the published image instead."; \
		fi; \
	else \
		TUTOR_WHY="no checkout at $$AI_DIR (set TUTOR_DIR in .env to point at it)"; \
		echo "No tutor checkout at $$AI_DIR — set TUTOR_DIR to its path, or pulling the published image instead."; \
	fi; \
	if [ "$$TUTOR_READY" = "0" ]; then \
		if docker compose $(1) pull tutor-service; then \
			TUTOR_READY=1; \
		else \
			echo; \
			echo "=============================================================="; \
			echo " THE APP IS STARTING WITHOUT THE AI TUTOR."; \
			echo " Reason: $$TUTOR_WHY"; \
			echo " The GHCR fallback also failed: the package is private, so the"; \
			echo " pull needs 'docker login ghcr.io -u <github-user>' with a PAT"; \
			echo " that has read:packages."; \
			echo "=============================================================="; \
			echo; \
		fi; \
	fi; \
	docker compose $(1) build $$(docker compose $(1) config --services | grep -v '^tutor-service$$'); \
	if [ "$$TUTOR_READY" = "1" ]; then \
			docker compose $(1) up --force-recreate; \
		else \
			docker compose $(1) up --force-recreate $$(docker compose $(1) config --services | grep -v '^tutor-service$$'); \
	fi 
endef

# First-run setup for Canvas: create its database and seed sample data. Does
# nothing on every subsequent run, so `make canvas` stays a single command you
# can re-run freely. Detection is one query -- to_regclass returns NULL (empty
# output) when the schema was never created. $(1) is the same extra
# `docker compose` flags run_dev_compose takes.
#
# Note there is no restart of `canvas`/`canvas-jobs` here even though they boot
# against an empty schema and cache nils: run_dev_compose finishes with
# `up --force-recreate`, which recreates them after this has run.
define canvas_bootstrap
	@set -e; \
	docker compose $(1) up -d canvas-postgres; \
	echo "Waiting for canvas-postgres..."; \
	attempts=0; \
	until docker compose $(1) exec -T canvas-postgres pg_isready -U canvas >/dev/null 2>&1; do \
		attempts=$$((attempts + 1)); \
		if [ $$attempts -ge 60 ]; then \
			echo "canvas-postgres did not become ready after 2 minutes."; \
			docker compose $(1) ps; \
			docker compose $(1) logs --tail 50 canvas-postgres; \
			exit 1; \
		fi; \
		sleep 2; \
	done; \
	if [ -z "$$(docker compose $(1) exec -T canvas-postgres psql -U canvas -d canvas -tAc "SELECT to_regclass('public.accounts')" 2>/dev/null | tr -d '[:space:]')" ]; then \
		echo "First run: creating the Canvas database. This takes several minutes."; \
		docker compose $(1) run --rm \
			-e CANVAS_LMS_ADMIN_EMAIL='$(CANVAS_ADMIN_EMAIL)' \
			-e CANVAS_LMS_ADMIN_PASSWORD='$(CANVAS_ADMIN_PASSWORD)' \
			-e CANVAS_LMS_ACCOUNT_NAME='$(CANVAS_ACCOUNT_NAME)' \
			-e CANVAS_LMS_STATS_COLLECTION=opt_out \
			canvas bundle exec rake db:create db:initial_setup; \
		echo "Seeding sample courses, users and assignments into Canvas..."; \
		docker compose $(1) run --rm canvas bundle exec rails runner /usr/src/app/canvas_seed.rb; \
		echo; \
		echo "=============================================================="; \
		echo " Canvas is set up."; \
		echo " Admin:  $(CANVAS_ADMIN_EMAIL) / $(CANVAS_ADMIN_PASSWORD)"; \
		echo " Seeded users all use the password: password123"; \
		echo; \
		echo " Still to do once, by hand:"; \
		echo "  1. log in at localhost:3001, Account -> Settings -> New Access Token"; \
		echo "  2. edit the seeded Canvas provider platform in UnlockEd:"; \
		echo "       base_url   http://canvas   (NOT localhost:3001)"; \
		echo "       account_id 1"; \
		echo "       access_key the token from step 1"; \
		echo "=============================================================="; \
		echo; \
	else \
		echo "Canvas database already set up, skipping init."; \
	fi
endef
SEED_MAIN=backend/seeder/main.go
BINARY_NAME=server
GOARCH=$(shell go env GOARCH)
GOOS=$(shell go env GOOS)
ACCOUNT_ID=$(AWS_ACCOUNT_ID)
REPO=.dkr.ecr.us-west-2.amazonaws.com

ascii_art:
	@echo ' ____ ___      .__                 __              .___     ________  '
	@echo '|    |   \____ |  |   ____   ____ |  | __ ____   __| _/__  _\_____  \ '
	@echo '|    |   /    \|  |  /  _ \_/ ___\|  |/ // __ \ / __ |\  \/ //  ____/ '
	@echo '|    |  /   |  \  |_(  <_> )  \___|    <\  ___// /_/ | \   //       \ '
	@echo '|______/|___|  /____/\____/ \___  >__|_ \\___  >____ |  \_/ \_______ \'
	@echo '             \/                 \/     \/    \/     \/              \/'


.PHONY: help prod dev dev-registry dev-tutor migrate-fresh seed build-binaries init kolibri canvas canvas-init canvas-seed migrate reset migration install-dep


help: ascii_art
	@echo " ⚡Usage: make [target] ⚡"
	@echo " Targets:"
	@echo " ⚡ init                   Install initial development dependencies for the project"
	@echo "   dev                    Run containers in development mode with hot-reloading for server and frontend only"
	@echo "   dev-tutor              Like dev, but with live hot-reload for tutor source (requires sibling checkout / symlink)"
	@echo "   dev-registry           Like dev, but pulls the tutor image from GHCR instead of building it (no sibling checkout needed)"
	@echo " 󱗆  kolibri                Run all containers with Kolibri (requires login to UL ECR | team only)"
	@echo " 󰂺  canvas                 Run all containers alongside Canvas LMS (Canvas at localhost:3001)"
	@echo "                           First run also creates + seeds the Canvas DB; later runs skip it"
	@echo " 󰂺  canvas-init            Create the Canvas DB by hand (only needed after 'down --volumes')"
	@echo " 󰂺  canvas-seed            Re-seed sample users/courses/assignments into Canvas (idempotent)"
	@echo "   migrate                Apply the migrations"
	@echo "   migrate-fresh          Drop the tables in the main application and to reset the database to a fresh state"
	@echo "   migration NAME=x       Create a new migration with the provided name"
	@echo " 󱗆  install-dep NAME=x     Install a dependency on the front-end while the containers are running"
	@echo " 󱘤  seed                   Run the seeder script"
	@echo "   build                  Build Go binaries for different platforms"
	@echo " 󰑙  reset                  Drop all volumes and reset all data in the database"
	@echo " ➕ merge PR=x             Merge a PR (requires GitHub CLI and permissions | team only)"


reset: ascii_art
	docker compose down --volumes

init: ascii_art
	@echo 'Installing dependencies...'
	go install github.com/pressly/goose/v3/cmd/goose@latest && go install github.com/air-verse/air@latest && cd frontend && yarn install && cd ..
	@echo 'Dependencies installed successfully.'
	$(MAKE) dev

dev: ascii_art
	$(call run_dev_compose,)

# Like `dev`, but pulls the tutor image from GHCR instead of building it from a sibling
# checkout. Needs `docker login ghcr.io` unless the package is Internal/Public.
dev-registry: ascii_art
	docker compose pull tutor-service
	docker compose up --force-recreate

# Like `dev`, but mounts the sibling tutor checkout as a live volume so Next.js
# HMR works — no rebuild needed when you edit tutor source files.
dev-tutor: ascii_art
	docker compose -f docker-compose.yml -f docker-compose.dev-tutor.yml up $(BUILD_RECREATE)

install-dep: ascii_art
	@if [ -z "$(NAME)" ]; then \
		echo "Error: NAME is not set, please provide package name (make install-dep NAME=some_pkg)"; \
		exit 1; \
	fi
	docker compose down && docker volume rm -f unlockedv2_node_modules && cd frontend && yarn add $(NAME) && cd ..
	$(call run_dev_compose,)
migrate-fresh: ascii_art
	go run -tags dev $(MIGRATE_MAIN) --fresh

migrate: ascii_art
	go run $(MIGRATE_MAIN)

kolibri: ascii_art
	$(call run_dev_compose,-f $(DOCKER_COMPOSE) -f $(KOLIBRI_COMPOSE))

# Run the normal stack PLUS Canvas LMS. The Canvas images are only pulled and
# started here, never during `make init` / `make dev`. Canvas gets its own
# postgres + redis (see config/docker-compose.canvas.yml) so it never touches
# the unlocked database. The first run also creates and seeds the Canvas
# database; later runs skip straight to starting the containers.
canvas: ascii_art
	$(call canvas_bootstrap,-f $(DOCKER_COMPOSE) -f $(CANVAS_COMPOSE))
	$(call run_dev_compose,-f $(DOCKER_COMPOSE) -f $(CANVAS_COMPOSE))

# Canvas database creation + initial setup. `make canvas` does this for you on
# the first run -- this target is here for doing it by hand, e.g. to re-create
# the database after `down --volumes`. Prompts interactively for the admin email
# and password (`make canvas` passes them non-interactively instead).
canvas-init: ascii_art
	docker compose -f $(DOCKER_COMPOSE) -f $(CANVAS_COMPOSE) run --rm canvas \
		bundle exec rake db:create db:initial_setup

# Seed sample data (users / courses / enrollments / assignments) into Canvas.
# Also run by `make canvas` on the first run. Idempotent -- safe to re-run by
# hand to top the data back up. All seeded users have password: password123
canvas-seed: ascii_art
	docker compose -f $(DOCKER_COMPOSE) -f $(CANVAS_COMPOSE) run --rm canvas \
		bundle exec rails runner /usr/src/app/canvas_seed.rb

seed: ascii_art
	go run $(SEED_MAIN)

build: ascii_art
	@if [ -z "$(ACCOUNT_ID)" ]; then \
		echo "Error: ACCOUNT_ID is not set, you must set AWS_ACCOUNT_ID env var, or pass ACCOUNT_ID=value in order to tag builds"; \
		exit 1; \
	fi
	docker buildx build . -f backend/Dockerfile --tag=$(ACCOUNT_ID)$(REPO)/unlockedv2:latest  && docker buildx build frontend -f frontend/Dockerfile --tag=$(ACCOUNT_ID)$(REPO)/frontend:latest

migration: ascii_art
	@if [ -z "$(NAME)" ]; then \
		echo "Error: NAME is not set"; \
		exit 1; \
	fi
	@echo "Creating migration with name $(NAME)..."
	goose -dir backend/migrations create $(NAME) sql
	goose -dir backend/migrations fix

merge: ascii_art
	@if [-z "$(PR)" ];then \
		echo "Error: PR is not set"; \
		exit 1; \
	fi
	@echo "Merging PR $(PR)..."
	./.github/merge.sh $(PR)
