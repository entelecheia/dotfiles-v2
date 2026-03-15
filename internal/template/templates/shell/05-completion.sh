# ── Zsh Completion 설정 ──
# Oh My Zsh compinit 이후 적용되는 zstyle 설정

# menu select에 필요한 모듈
zmodload zsh/complist

# 메뉴 선택 모드: Tab으로 후보 순환, 화살표 키로 선택
zstyle ':completion:*' menu select

# 대소문자 무시 + 하이픈/언더스코어 무시
zstyle ':completion:*' matcher-list 'm:{a-zA-Z-_}={A-Za-z_-}' 'r:|=*' 'l:|=* r:|=*'

# 후보 목록을 컬러로 표시 (ls --color와 동일)
zstyle ':completion:*' list-colors "${(s.:.)LS_COLORS}"

# 현재 선택 항목 하이라이트
zstyle ':completion:*' list-prompt '%SAt %p: Hit TAB for more, or the character to insert%s'

# 후보가 많을 때 페이지 단위 스크롤
zstyle ':completion:*' select-prompt '%SScrolling active: current selection at %p%s'

# 디렉토리 완성 시 trailing slash 자동 추가
zstyle ':completion:*' special-dirs true

# 그룹별로 후보 정리 (files, directories, commands 등)
zstyle ':completion:*' group-name ''
zstyle ':completion:*:descriptions' format '%B%F{cyan}── %d ──%f%b'

# cd 완성 시 부모 디렉토리 제외 (. 과 .. 은 이미 있으므로)
zstyle ':completion:*:cd:*' ignore-parents parent pwd

# 캐시 활성화 (느린 완성 가속)
zstyle ':completion:*' use-cache on
zstyle ':completion:*' cache-path "$HOME/.zcompcache"
