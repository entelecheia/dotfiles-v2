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

add-secret: ## Add encrypted file: make add-secret FILE=~/.ssh/id_ed25519
	chezmoi add --encrypt $(FILE)

re-encrypt: ## Re-encrypt all secrets
	chezmoi re-add

test-ubuntu: ## Test in Ubuntu Docker
	docker run --rm -it -v $(CURDIR):/dotfiles -w /dotfiles \
		-e CHEZMOI_ARGS="--no-tty --promptDefaults" \
		-e DOTFILES_REPO="local" \
		ubuntu:24.04 bash -c "apt-get update && apt-get install -y curl git sudo && bash scripts/bootstrap.sh"

help: ## Show this help
	@grep -E '^[a-z_-]+:.*## ' Makefile | awk -F ':.*## ' '{printf "  %-16s %s\n", $$1, $$2}'
