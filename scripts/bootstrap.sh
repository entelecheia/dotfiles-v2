#!/usr/bin/env bash
set -euo pipefail

REPO="${DOTFILES_REPO:-entelecheia/dotfiles-v2}"
BRANCH="${DOTFILES_BRANCH:-main}"

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

OS=$(uname -s | tr '[:upper:]' '[:lower:]')

# ── 현재 설정 표시 ──
show_config() {
  local config="$1"
  if [ ! -f "$config" ]; then
    return 1
  fi

  echo ""
  echo -e "${BOLD}  Current Configuration${NC}"
  line

  local name email github_user timezone profile
  local enable_workspace enable_ai_tools enable_warp ssh_key_name has_age_key

  name=$(yq -r '.data.name // "N/A"' "$config" 2>/dev/null || echo "N/A")
  email=$(yq -r '.data.email // "N/A"' "$config" 2>/dev/null || echo "N/A")
  github_user=$(yq -r '.data.github_user // "N/A"' "$config" 2>/dev/null || echo "N/A")
  timezone=$(yq -r '.data.timezone // "N/A"' "$config" 2>/dev/null || echo "N/A")
  profile=$(yq -r '.data.profile // "N/A"' "$config" 2>/dev/null || echo "N/A")
  enable_workspace=$(yq -r '.data.enable_workspace // false' "$config" 2>/dev/null || echo "false")
  enable_ai_tools=$(yq -r '.data.enable_ai_tools // false' "$config" 2>/dev/null || echo "false")
  enable_warp=$(yq -r '.data.enable_warp // false' "$config" 2>/dev/null || echo "false")
  ssh_key_name=$(yq -r '.data.ssh_key_name // "N/A"' "$config" 2>/dev/null || echo "N/A")
  has_age_key=$(yq -r '.data.has_age_key // false' "$config" 2>/dev/null || echo "false")

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

  return 0
}

# ── 1. Homebrew 설치 ──
if ! command -v brew &>/dev/null; then
  info "Installing Homebrew..."
  NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

  if [ "$OS" = "darwin" ]; then
    eval "$(/opt/homebrew/bin/brew shellenv)"
  else
    eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"
  fi
fi

# ── 2. Linux 전제조건 ──
if [ "$OS" = "linux" ]; then
  sudo apt-get update -qq
  sudo apt-get install -y -qq build-essential procps curl file git zsh
fi

# ── 3. Chezmoi 설치 ──
if ! command -v chezmoi &>/dev/null; then
  info "Installing chezmoi..."
  brew install chezmoi
fi

# yq 설치 (설정 표시용)
if ! command -v yq &>/dev/null; then
  brew install yq
fi

# ── 4. 기존 설정 확인 + 재설정 여부 ──
CONFIG="$HOME/.config/chezmoi/chezmoi.yaml"
CHEZMOI_ARGS=""

if show_config "$CONFIG"; then
  echo -en "  ${YELLOW}Reconfigure?${NC} [y/N] "
  read -r answer
  if [[ "$answer" =~ ^[Yy] ]]; then
    CHEZMOI_ARGS="--prompt"
    info "Starting reconfiguration..."
  else
    info "Keeping current settings."
  fi
  echo ""
else
  echo ""
  echo -e "${BOLD}  First-time Setup${NC}"
  line
  echo "  You'll be asked a few questions to configure your environment."
  echo ""
fi

# ── 5. 초기화 ──
info "Initializing dotfiles from $REPO..."
chezmoi init "$REPO" --branch "$BRANCH" --apply $CHEZMOI_ARGS

# ── 6. 결과 표시 ──
if [ -f "$CONFIG" ]; then
  echo ""
  echo -e "${GREEN}${BOLD}  Applied Configuration${NC}"
  show_config "$CONFIG"
fi

info "Done! Restart shell: exec zsh"
