.DEFAULT_GOAL := help

apply: ## Apply dotfiles to this machine
	chezmoi apply -v

update: ## Pull latest + apply
	chezmoi update -v

diff: ## Preview pending changes
	chezmoi diff

edit: ## Edit chezmoi config
	chezmoi edit-config

add: ## Add a file: make add FILE=~/.config/foo
	chezmoi add $(FILE)

secrets-init: ## Encrypt secrets to ~/.local/share/chezmoi-secrets/
	bash scripts/secrets.sh init

secrets-backup: ## Backup secrets: make secrets-backup DEST=~/backup
	bash scripts/secrets.sh backup $(DEST)

secrets-restore: ## Restore secrets: make secrets-restore SRC=~/backup
	bash scripts/secrets.sh restore $(SRC)

secrets-status: ## Check secrets status
	bash scripts/secrets.sh status

test-ubuntu: ## Test in Ubuntu Docker
	docker run --rm -it -v $(CURDIR):/dotfiles -w /dotfiles \
		-e CHEZMOI_ARGS="--no-tty --promptDefaults" \
		-e DOTFILES_REPO="local" \
		ubuntu:24.04 bash -c "apt-get update && apt-get install -y curl git sudo && bash scripts/bootstrap.sh"

help: ## Show this help
	@grep -E '^[a-z_-]+:.*## ' Makefile | awk -F ':.*## ' '{printf "  %-20s %s\n", $$1, $$2}'
