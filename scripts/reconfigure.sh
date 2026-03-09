#!/usr/bin/env bash
set -euo pipefail

# ── Colors ──
GREEN='\033[0;32m'
CYAN='\033[0;36m'
YELLOW='\033[1;33m'
DIM='\033[2m'
BOLD='\033[1m'
NC='\033[0m'

info()  { echo -e "${GREEN}[+]${NC} $*"; }
label() { printf "  ${CYAN}%-14s${NC} %s\n" "$1" "$2"; }
line()  { echo -e "${DIM}──────────────────────────────────────────${NC}"; }

CONFIG="$HOME/.config/chezmoi/chezmoi.yaml"

if [ ! -f "$CONFIG" ]; then
  echo -e "${YELLOW}[!]${NC} No existing configuration found."
  echo "    Run bootstrap first: scripts/bootstrap.sh"
  exit 1
fi

if ! command -v yq &>/dev/null; then
  echo -e "${YELLOW}[!]${NC} yq is required. Install: brew install yq"
  exit 1
fi

# ── 현재 설정 표시 ──
echo ""
echo -e "${BOLD}  Current Configuration${NC}"
line

name=$(yq -r '.data.name // "N/A"' "$CONFIG")
email=$(yq -r '.data.email // "N/A"' "$CONFIG")
github_user=$(yq -r '.data.github_user // "N/A"' "$CONFIG")
timezone=$(yq -r '.data.timezone // "N/A"' "$CONFIG")
profile=$(yq -r '.data.profile // "N/A"' "$CONFIG")
enable_workspace=$(yq -r '.data.enable_workspace // false' "$CONFIG")
enable_ai_tools=$(yq -r '.data.enable_ai_tools // false' "$CONFIG")
enable_warp=$(yq -r '.data.enable_warp // false' "$CONFIG")
ssh_key_name=$(yq -r '.data.ssh_key_name // "N/A"' "$CONFIG")
has_age_key=$(yq -r '.data.has_age_key // false' "$CONFIG")

label "Name:" "$name"
label "Email:" "$email"
label "GitHub:" "$github_user"
label "Timezone:" "$timezone"
label "Profile:" "$profile"
label "Workspace:" "$enable_workspace"
label "AI Tools:" "$enable_ai_tools"
label "Warp:" "$enable_warp"
label "SSH Key:" "$ssh_key_name"
label "Encryption:" "$has_age_key"
line
echo ""

echo -en "  ${YELLOW}Reconfigure?${NC} [y/N] "
read -r answer
if [[ ! "$answer" =~ ^[Yy] ]]; then
  echo "  Cancelled."
  exit 0
fi

echo ""
info "Starting reconfiguration (current values shown as defaults)..."
echo ""

# ── 재설정: --prompt로 모든 값 재질문 ──
chezmoi init --prompt --apply

# ── 결과 표시 ──
echo ""
echo -e "${GREEN}${BOLD}  Updated Configuration${NC}"
line

name=$(yq -r '.data.name // "N/A"' "$CONFIG")
email=$(yq -r '.data.email // "N/A"' "$CONFIG")
github_user=$(yq -r '.data.github_user // "N/A"' "$CONFIG")
timezone=$(yq -r '.data.timezone // "N/A"' "$CONFIG")
profile=$(yq -r '.data.profile // "N/A"' "$CONFIG")
enable_workspace=$(yq -r '.data.enable_workspace // false' "$CONFIG")
enable_ai_tools=$(yq -r '.data.enable_ai_tools // false' "$CONFIG")
enable_warp=$(yq -r '.data.enable_warp // false' "$CONFIG")
ssh_key_name=$(yq -r '.data.ssh_key_name // "N/A"' "$CONFIG")
has_age_key=$(yq -r '.data.has_age_key // false' "$CONFIG")

label "Name:" "$name"
label "Email:" "$email"
label "GitHub:" "$github_user"
label "Timezone:" "$timezone"
label "Profile:" "$profile"
label "Workspace:" "$enable_workspace"
label "AI Tools:" "$enable_ai_tools"
label "Warp:" "$enable_warp"
label "SSH Key:" "$ssh_key_name"
label "Encryption:" "$has_age_key"
line
echo ""

info "Done! Restart shell to apply: exec zsh"
