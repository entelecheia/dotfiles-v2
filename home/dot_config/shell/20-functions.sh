# 환경 파일 관리
lnenv() {
  local project="${1:-$(basename "$PWD")}"
  local envfile="${2:-.env}"
  local shared="${envfile#.}"
  local src="$HOME/.local/share/envfiles/$project/$shared"
  [ -f "$envfile" ] && { echo "$envfile already exists."; return 1; }
  [ -f "$src" ] || { echo "Source not found: $src"; return 1; }
  ln -s "$src" "$envfile"
  echo "Linked $src -> $envfile"
}

mvenv() {
  local project="${1:-$(basename "$PWD")}"
  local envfile="${2:-.env}"
  local shared="${envfile#.}"
  local dest="$HOME/.local/share/envfiles/$project/$shared"
  [ -L "$envfile" ] && { echo "$envfile is already a symlink."; return 1; }
  [ -f "$dest" ] && { echo "Destination exists: $dest"; return 1; }
  [ -f "$envfile" ] || touch "$envfile"
  mkdir -p "$(dirname "$dest")"
  mv "$envfile" "$dest"
  ln -s "$dest" "$envfile"
  echo "Moved $envfile -> $dest (symlinked)"
}

# mkcd: 디렉토리 생성 + 이동
mkcd() { mkdir -p "$1" && cd "$1"; }

# extract: 아카이브 범용 해제
extract() {
  case "$1" in
    *.tar.gz|*.tgz)  tar xzf "$1" ;;
    *.tar.bz2|*.tbz) tar xjf "$1" ;;
    *.tar.xz)        tar xJf "$1" ;;
    *.zip)           unzip "$1" ;;
    *.gz)            gunzip "$1" ;;
    *)               echo "Unknown format: $1" ;;
  esac
}
