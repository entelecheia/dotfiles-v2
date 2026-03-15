# dotfiles-v2

[![Test](https://github.com/entelecheia/dotfiles-v2/actions/workflows/test.yaml/badge.svg)](https://github.com/entelecheia/dotfiles-v2/actions/workflows/test.yaml)
[![Release](https://github.com/entelecheia/dotfiles-v2/actions/workflows/release.yaml/badge.svg)](https://github.com/entelecheia/dotfiles-v2/actions/workflows/release.yaml)

Declarative user environment management — a single Go binary that replaces chezmoi.
macOS + Linux. Modular, profile-based, AI-ready.

> 선언적 사용자 환경 관리 — chezmoi를 대체하는 단일 Go 바이너리.
> macOS + Linux. 모듈 기반, 프로필 시스템, AI-ready.

---

## Quick Start | 빠른 시작

### Install | 설치

```bash
# Download prebuilt binary from GitHub Releases
# GitHub Releases에서 빌드된 바이너리 다운로드
curl -fsSL https://raw.githubusercontent.com/entelecheia/dotfiles-v2/main/scripts/install.sh | bash
```

### Setup | 초기 설정

```bash
# Interactive TUI setup — prompts for name, email, profile, etc.
# 대화형 TUI 설정 — 이름, 이메일, 프로필 등을 입력
dotfiles init

# Apply all modules for selected profile
# 선택한 프로필의 모든 모듈 적용
dotfiles apply
```

### Build from source | 소스에서 빌드

```bash
git clone https://github.com/entelecheia/dotfiles-v2.git
cd dotfiles-v2
make build      # → bin/dotfiles
make install    # → ~/.local/bin/dotfiles
```

---

## Commands | 명령어

### `dotfiles init`

Interactive TUI setup. Collects user info and saves to `~/.config/dotfiles/config.yaml`.

대화형 TUI 설정. 사용자 정보를 수집하여 `~/.config/dotfiles/config.yaml`에 저장합니다.

```bash
dotfiles init
```

Prompts for | 입력 항목:
- Name, Email, GitHub username
- Timezone (default: `Asia/Seoul`)
- Profile (`minimal` / `full`)
- Module opt-ins: workspace, AI tools, Warp, fonts
- SSH key name (auto-derived from GitHub username)

### `dotfiles apply`

Apply configuration to the system. Runs each enabled module in order.

시스템에 설정을 적용합니다. 활성화된 모듈을 순서대로 실행합니다.

```bash
dotfiles apply                           # interactive
dotfiles apply --yes                     # unattended (skip prompts)
dotfiles apply --dry-run                 # preview only, no changes
dotfiles apply --profile minimal         # override profile
dotfiles apply --module shell --module git  # specific modules only
```

### `dotfiles check`

Compare current system state against desired profile. No changes made.

현재 시스템 상태를 원하는 프로필과 비교합니다. 변경 없음.

```bash
dotfiles check
dotfiles check --profile full
```

Output | 출력:
```
MODULE          STATUS     CHANGES
packages        OK         0 change(s)
shell           PENDING    3 change(s)
  → write ~/.config/shell/00-exports.sh
  → write ~/.config/shell/50-tools-init.sh
  → download/refresh oh-my-zsh
git             OK         0 change(s)
```

### `dotfiles diff`

Preview pending changes with detailed descriptions and commands.

변경 사항을 상세 설명과 명령어와 함께 미리보기합니다.

```bash
dotfiles diff
dotfiles diff --module git
```

### `dotfiles update`

Pull latest changes from the dotfiles repo and re-apply.

dotfiles 저장소에서 최신 변경을 가져와 재적용합니다.

```bash
dotfiles update
```

### `dotfiles reconfigure`

Re-run the init prompts with current values as defaults, then optionally re-apply.

현재 값을 기본값으로 설정 프롬프트를 다시 실행하고, 선택적으로 재적용합니다.

```bash
dotfiles reconfigure
```

### `dotfiles secrets`

Manage age-encrypted secrets (SSH keys, shell secrets).

age 암호화 기밀(SSH 키, 셸 시크릿) 관리.

```bash
dotfiles secrets init              # encrypt SSH key + shell secrets
dotfiles secrets backup <dir>      # copy .age files to backup dir
dotfiles secrets restore <dir>     # decrypt from backup
dotfiles secrets status            # check decrypted + encrypted files
dotfiles secrets list              # list encrypted files
```

**Encryption flow | 암호화 흐름:**
```
~/.ssh/id_ed25519_user  →  age -e  →  ~/.local/share/dotfiles-secrets/id_ed25519_user.age
~/.config/shell/90-secrets.sh  →  age -e  →  ~/.local/share/dotfiles-secrets/90-secrets.sh.age
```

**Restore flow | 복원 흐름:**
```
backup/id_ed25519_user.age  →  age -d  →  ~/.ssh/id_ed25519_user
backup/90-secrets.sh.age    →  age -d  →  ~/.config/shell/90-secrets.sh
```

### `dotfiles version`

Print version, commit, Go version, and OS/arch.

버전, 커밋, Go 버전, OS/아키텍처를 출력합니다.

```bash
dotfiles version
# dotfiles v0.1.0 (abc1234)
#   go:   go1.23.0
#   os:   darwin/arm64
```

### Global Flags | 전역 플래그

| Flag | Description | 설명 |
|------|-------------|------|
| `--profile` | Profile name (`minimal`, `full`) | 프로필 이름 |
| `--module` | Run specific modules only (repeatable) | 특정 모듈만 실행 (반복 가능) |
| `--dry-run` | Preview without changes | 변경 없이 미리보기 |
| `--yes` | Unattended mode (skip prompts) | 무인 모드 (프롬프트 생략) |
| `--config` | Custom config YAML path | 커스텀 설정 YAML 경로 |

---

## Modules | 모듈

### Execution Order | 실행 순서

```
packages → shell → git → ssh → terminal → tmux →
workspace → ai-tools → fonts → conda → gpg → secrets
```

### Module Details | 모듈 상세

| Module | Profile | Description | 설명 |
|--------|---------|-------------|------|
| **packages** | minimal | Homebrew formula installation | Homebrew 패키지 설치 |
| **shell** | minimal | zsh, Oh My Zsh, plugins, config files | zsh, Oh My Zsh, 플러그인, 설정 파일 |
| **git** | minimal | git config, aliases, global ignore | git 설정, 별칭, 전역 무시 |
| **ssh** | minimal | SSH config, config.d includes | SSH 설정, config.d 포함 |
| **terminal** | minimal | starship prompt, Warp theme (macOS) | starship 프롬프트, Warp 테마 |
| **tmux** | full | tmux.conf (256color, vim keys, C-a prefix) | tmux 설정 |
| **workspace** | full | Symlink federation (Google Drive, vault) | 심링크 통합 (Google Drive, vault) |
| **ai-tools** | full | Claude Code config, GitHub Models aliases | Claude Code, GitHub Models 별칭 |
| **fonts** | full | Nerd Font download from GitHub Releases | Nerd Font 자동 다운로드/설치 |
| **conda** | full | Conda/Mamba shell initialization | Conda/Mamba 셸 초기화 |
| **gpg** | full | GPG agent + git commit signing | GPG 에이전트 + git 서명 |
| **secrets** | full | Age-encrypted SSH keys and shell secrets | age 암호화 SSH 키/시크릿 |

### Packages | 패키지 목록

**minimal** (15):
`git`, `git-lfs`, `gh`, `age`, `fzf`, `ripgrep`, `fd`, `bat`, `jq`, `yq`, `direnv`, `zoxide`, `eza`, `starship`, `curl`

**full** adds (+11):
`btop`, `lazygit`, `yazi`, `glow`, `csvlens`, `chafa`, `fnm`, `uv`, `pipx`, `tmux`, `gnupg`

---

## Profiles | 프로필

Profiles use YAML inheritance. `full` extends `minimal`.

프로필은 YAML 상속을 사용합니다. `full`은 `minimal`을 확장합니다.

| Profile | Modules | Packages | Use Case | 사용 사례 |
|---------|---------|----------|----------|-----------|
| **minimal** | 5 | 15 | Lightweight dev setup | 경량 개발 환경 |
| **full** | 12 | 26+ | Complete workstation | 완전한 워크스테이션 |

---

## Configuration | 설정

User settings are stored in `~/.config/dotfiles/config.yaml`:

사용자 설정은 `~/.config/dotfiles/config.yaml`에 저장됩니다:

```yaml
name: "Young Joon Lee"
email: "hello@jeju.ai"
github_user: "entelecheia"
timezone: "Asia/Seoul"
profile: "full"
modules:
  workspace:
    path: "~/ai-workspace"
    gdrive: "~/My Drive (hello@jeju.ai)"
  ai_tools: true
  warp: false
  fonts:
    family: "FiraCode"
ssh:
  key_name: "id_ed25519_entelecheia"
secrets:
  age_identity: "~/.ssh/age_key_entelecheia"
  age_recipients:
    - "age1..."
```

### Environment Variables | 환경 변수

| Variable | Description | 설명 |
|----------|-------------|------|
| `DOTFILES_YES` | Set to `true` for unattended mode | `true`로 설정하면 무인 모드 |
| `DOTFILES_PROFILE` | Override profile name | 프로필 이름 재정의 |
| `DOTFILES_NAME` | Override user name | 사용자 이름 재정의 |
| `DOTFILES_EMAIL` | Override email | 이메일 재정의 |
| `DOTFILES_WORKSPACE_PATH` | Override workspace path | 워크스페이스 경로 재정의 |
| `DOTFILES_REPO_DIR` | Dotfiles repo directory for `update` | `update` 명령의 저장소 경로 |

---

## Architecture | 아키텍처

Same modular Go architecture as [rootfiles-v2](https://github.com/entelecheia/rootfiles-v2).

[rootfiles-v2](https://github.com/entelecheia/rootfiles-v2)와 동일한 모듈형 Go 아키텍처.

```
rootfiles-v2 (root, server)     dotfiles-v2 (user, workstation)
━━━━━━━━━━━━━━━━━━━━━━━━━━━     ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Packages (APT), users, SSH       Packages (Homebrew), shell, git
Docker, GPUs, tunnels            Terminal, fonts, AI tools
Locale, firewall, storage        Workspace, secrets, tmux
```

### Project Structure | 프로젝트 구조

```
dotfiles-v2/
├── cmd/dotfiles/main.go          # Entry point (ldflags: version, commit)
├── internal/
│   ├── cli/                      # Cobra commands (9 files)
│   ├── config/                   # Config struct, loader, detector, state
│   │   └── profiles/             # Embedded YAML profiles (go:embed)
│   ├── exec/                     # Runner (dry-run), Brew wrapper
│   ├── module/                   # 12 module implementations
│   ├── template/                 # Go text/template engine
│   │   └── templates/            # Embedded templates (go:embed)
│   ├── fileutil/                 # File ops, download, hash compare
│   └── ui/                       # Charm huh TUI wrapper
├── scripts/install.sh            # curl-pipe installer
├── .goreleaser.yaml              # Cross-platform release config
└── .github/workflows/            # CI: test → release pipeline
```

### Key Design | 핵심 설계

- **Module interface**: `Check()` → `Apply()` — idempotent, dry-run aware
- **Profile inheritance**: YAML `extends` chain with field-level merging
- **go:embed**: Profiles and templates compiled into the binary
- **SHA256 hash**: Skip writes when content unchanged, backup before overwrite
- **Non-fatal errors**: Module failures logged, remaining modules continue

---

## CI/CD

- **Test**: Runs on push to `main`, version tags, and PRs. Matrix: `ubuntu-latest` + `macos-latest`.
- **Release**: Triggered by `workflow_run` — only after Test succeeds on a `v*` tag. Uses GoReleaser for cross-platform builds (darwin/linux × amd64/arm64).

### Creating a Release | 릴리스 생성

```bash
git tag v0.1.0
git push origin v0.1.0
# Test workflow runs → on success → Release workflow creates GitHub Release
```

---

## Development | 개발

```bash
make build      # Build → bin/dotfiles
make test       # go test ./... -race
make lint       # golangci-lint
make clean      # Remove bin/
make install    # Copy to ~/.local/bin/
```

## License

MIT
