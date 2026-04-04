# dotfiles-v2

[![Test](https://github.com/entelecheia/dotfiles-v2/actions/workflows/test.yaml/badge.svg)](https://github.com/entelecheia/dotfiles-v2/actions/workflows/test.yaml)
[![Release](https://github.com/entelecheia/dotfiles-v2/actions/workflows/release.yaml/badge.svg)](https://github.com/entelecheia/dotfiles-v2/actions/workflows/release.yaml)

Declarative user environment management + AI-powered tmux workspace manager.
A single Go binary. macOS + Linux + GPU servers. Modular, profile-based, AI-ready.

> 선언적 사용자 환경 관리 + AI 기반 tmux 워크스페이스 매니저.
> 단일 Go 바이너리. macOS + Linux + GPU 서버. 모듈 기반, 프로필 시스템, AI-ready.

---

## Quick Start | 빠른 시작

### Install | 설치

```bash
# Download prebuilt binary from GitHub Releases
# GitHub Releases에서 빌드된 바이너리 다운로드
curl -fsSL https://raw.githubusercontent.com/entelecheia/dotfiles-v2/main/scripts/install.sh | bash
```

### Setup | 초기 설정

Interactive TUI setup — prompts for name, email, profile, etc.
대화형 TUI 설정 — 이름, 이메일, 프로필 등을 입력

```bash
dotfiles init
```

Apply all modules for selected profile.
선택한 프로필의 모든 모듈 적용.

```bash
dotfiles apply
```

### Workspace | 워크스페이스

Launch a multi-panel tmux workspace with one command.
한 줄 명령으로 멀티패널 tmux 워크스페이스 실행.

```bash
dot myproject          # launch or resume workspace
```

SSH dropped? Terminal closed? Just run it again.
SSH 끊김? 터미널 닫힘? 다시 실행하면 복귀.

```bash
dot myproject          # resumes exactly where you left off
```

### Build from source | 소스에서 빌드

```bash
git clone https://github.com/entelecheia/dotfiles-v2.git && cd dotfiles-v2
```

```bash
make build      # → bin/dotfiles
```

```bash
make install    # → ~/.local/bin/dotfiles + ~/.local/bin/dot (symlink)
```

---

## Commands | 명령어

> `dotfiles` and `dot` are interchangeable — `dot` is a convenience symlink.
> `dotfiles`와 `dot`은 동일하게 작동합니다 — `dot`은 편의 심링크입니다.

### `dotfiles init`

Interactive TUI setup. Collects user info and saves to `~/.config/dotfiles/config.yaml`.

대화형 TUI 설정. 사용자 정보를 수집하여 `~/.config/dotfiles/config.yaml`에 저장합니다.

```bash
dotfiles init
```

Prompts for | 입력 항목:
- Name, Email, GitHub username
- Timezone (default: `Asia/Seoul`)
- Profile (`minimal` / `full` / `server`)
- GPU/CUDA auto-detection (suggests `server` profile when NVIDIA GPU detected)
- Module opt-ins: workspace, AI tools, Warp, fonts
- SSH key name (auto-derived from GitHub username)

### `dotfiles apply`

Apply configuration to the system. Runs each enabled module in order.

시스템에 설정을 적용합니다. 활성화된 모듈을 순서대로 실행합니다.

Interactive mode | 대화형 모드:

```bash
dotfiles apply
```

Unattended (skip prompts) | 무인 모드 (프롬프트 생략):

```bash
dotfiles apply --yes
```

Preview only, no changes | 변경 없이 미리보기:

```bash
dotfiles apply --dry-run
```

Override profile | 프로필 재정의:

```bash
dotfiles apply --profile minimal
```

Specific modules only | 특정 모듈만 실행:

```bash
dotfiles apply --module shell --module git
```

#### Safe Apply — Preserve Current Environment | 안전한 적용 — 현재 환경 유지

To apply dotfiles without disrupting your current setup, follow this step-by-step workflow.

현재 환경을 유지하면서 dotfiles를 적용하려면 다음 단계를 따르세요.

**Step 1.** Run preflight checks to scan your environment (no changes made).
환경 사전 점검 실행 (변경 없음):

```bash
dotfiles preflight --check-only
```

**Step 2.** Generate a config file based on detected environment.
감지된 환경 기반으로 설정 파일 자동 생성:

```bash
dotfiles preflight
```

**Step 3.** Check which modules have pending changes.
어떤 모듈에 변경 사항이 있는지 확인:

```bash
dotfiles check
```

**Step 4.** Simulate the apply — preview all changes without writing.
적용 시뮬레이션 — 실제 변경 없이 미리보기:

```bash
dotfiles apply --dry-run
```

**Step 5.** Apply only the modules you want.
원하는 모듈만 선택 적용:

```bash
dotfiles apply --module shell
```

```bash
dotfiles apply --module git
```

> Existing files are automatically backed up to `~/.local/share/dotfiles/backup/` before overwrite. Files with identical content (SHA256) are never overwritten.
>
> 기존 파일은 덮어쓰기 전에 `~/.local/share/dotfiles/backup/`에 자동 백업됩니다. 내용이 동일한 파일(SHA256)은 덮어쓰지 않습니다.

### `dotfiles check`

Compare current system state against desired profile. No changes made.

현재 시스템 상태를 원하는 프로필과 비교합니다. 변경 없음.

```bash
dotfiles check
```

Check against a specific profile | 특정 프로필로 비교:

```bash
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
```

Diff for a specific module | 특정 모듈의 변경 사항:

```bash
dotfiles diff --module git
```

### `dotfiles upgrade`

Download and install the latest dotfiles release from GitHub. Self-upgrading binary.

GitHub에서 최신 dotfiles 릴리스를 다운로드하여 설치합니다. 자체 업그레이드 바이너리.

Download & install latest | 최신 버전 다운로드 및 설치:

```bash
dotfiles upgrade
```

Check for updates only | 업데이트 확인만:

```bash
dotfiles upgrade --check
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

Encrypt SSH key + shell secrets | SSH 키 + 셸 시크릿 암호화:

```bash
dotfiles secrets init
```

Copy .age files to backup dir | .age 파일을 백업 디렉토리에 복사:

```bash
dotfiles secrets backup <dir>
```

Decrypt from backup | 백업에서 복호화:

```bash
dotfiles secrets restore <dir>
```

Check decrypted + encrypted files | 복호화/암호화 파일 상태 확인:

```bash
dotfiles secrets status
```

List encrypted files | 암호화된 파일 목록:

```bash
dotfiles secrets list
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
```

Output example | 출력 예시:
```
dotfiles v0.1.0 (abc1234)
  go:   go1.23.0
  os:   darwin/arm64
```

### `dotfiles open` / `dot <project>`

Launch or resume a tmux workspace for a project. Auto-registers unregistered project names.

프로젝트의 tmux 워크스페이스를 시작하거나 복귀합니다. 미등록 프로젝트는 자동 등록됩니다.

```bash
dot myproject                         # implicit: dot open myproject
dot open myproject --layout claude    # override layout
dot open myproject --theme dracula    # override theme
```

### `dotfiles register` / `dotfiles unregister`

Register or remove a project directory.
프로젝트 디렉토리를 등록하거나 제거합니다.

```bash
dotfiles register myproject .                          # current dir
dotfiles register myproject ~/dev/app --layout claude  # with options
dotfiles unregister myproject                          # remove
```

### `dotfiles list`

Show registered projects and active tmux sessions. Active sessions marked with `*`.

등록된 프로젝트와 활성 tmux 세션을 표시합니다. 활성 세션은 `*`로 표시됩니다.

```bash
dotfiles list     # or: dotfiles ls
```

Output example | 출력 예시:
```
Projects (2):
  * myproject          ~/dev/app           (layout=dev, theme=default)
    server-mon         ~/ops/monitoring    (layout=monitor, theme=nord)
```

### `dotfiles stop`

Stop a tmux workspace session.
tmux 워크스페이스 세션을 종료합니다.

```bash
dotfiles stop myproject       # with confirmation
dotfiles stop myproject -f    # force (no confirmation)
```

### `dotfiles layouts`

List available workspace layouts.
사용 가능한 워크스페이스 레이아웃 목록을 표시합니다.

```bash
dotfiles layouts
```

| Layout | Panes | Description | 설명 |
|--------|-------|-------------|------|
| **dev** (default) | 5 | Claude + monitor + files + lazygit + shell | 노트북 친화 개발 환경 |
| **claude** | 7 | Claude + monitor + files + remote + lazygit + shell + logs | Claude 중심 환경 |
| **monitor** | 4 | monitor + lazygit + shell + logs | 서버 모니터링 |

### `dotfiles doctor`

Check workspace tool installation status.
워크스페이스 도구 설치 상태를 점검합니다.

```bash
dotfiles doctor
```

Output example | 출력 예시:
```
Workspace tool status:

  ✓ tmux         /opt/homebrew/bin/tmux
  ✓ claude       /usr/local/bin/claude
  ✓ lazygit      /opt/homebrew/bin/lazygit
  ✓ btop         /opt/homebrew/bin/btop
  ○ yazi         (optional — fallback available)
  ✓ eza          /opt/homebrew/bin/eza
```

### Global Flags | 전역 플래그

| Flag | Description | 설명 |
|------|-------------|------|
| `--profile` | Profile name (`minimal`, `full`, `server`) | 프로필 이름 |
| `--module` | Run specific modules only (repeatable) | 특정 모듈만 실행 (반복 가능) |
| `--dry-run` | Preview without changes | 변경 없이 미리보기 |
| `--yes` | Unattended mode (skip prompts) | 무인 모드 (프롬프트 생략) |
| `--config` | Custom config YAML path | 커스텀 설정 YAML 경로 |
| `--home` | Override home directory (admin setup) | 홈 디렉토리 재정의 (관리자 설정) |

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

## Tmux | tmux 설정

### Key Bindings | 키바인딩

| Key | Action | 설명 |
|-----|--------|------|
| `C-a` | Prefix | 프리픽스 키 |
| `C-a d` | Detach session | 세션 분리 |
| `C-a s` | List sessions | 세션 목록 |
| `C-a $` | Rename session | 세션 이름 변경 |
| `C-a c` | New window (current path) | 새 창 (현재 경로) |
| `C-a ,` | Rename window | 창 이름 변경 |
| `C-a n/p` | Next / previous window | 다음 / 이전 창 |
| `C-a </>` | Swap window left / right | 창 순서 이동 |
| `C-a \|` | Split horizontal | 수평 분할 |
| `C-a -` | Split vertical | 수직 분할 |
| `C-a h/j/k/l` | Navigate panes | 패인 이동 |
| `C-a H/J/K/L` | Resize panes | 패인 크기 조절 |
| `C-a Enter` | Enter copy mode | 복사 모드 진입 |
| `v` (copy mode) | Begin selection | 선택 시작 |
| `y` (copy mode) | Copy and exit | 복사 후 종료 |
| `Escape` (copy mode) | Cancel | 취소 |
| `C-a r` | Reload config | 설정 다시 로드 |
| `C-a ?` | List all key bindings | 전체 키바인딩 목록 |
| `C-a /` | Show cheatsheet popup | 치트시트 팝업 표시 |

### Shell Aliases | 셸 별칭

| Alias | Command | 설명 |
|-------|---------|------|
| `t [name]` | Attach or create session | 세션 접속 또는 생성 (기본: `main`) |
| `ta <name>` | `tmux attach -t` | 세션 접속 |
| `ts <name>` | `tmux new-session -s` | 새 세션 |
| `tl` | `tmux list-sessions` | 세션 목록 |
| `tk <name>` | `tmux kill-session -t` | 세션 종료 |
| `td` | `tmux detach` | 세션 분리 |

### Workspace Layouts | 워크스페이스 레이아웃

**dev** (default — 5 panes):
```
┌──────────────┬──────────┐
│              │  MONITOR │
│   CLAUDE     ├──────────┤
│              │  FILES   │
├──────────────┼──────────┤
│  LAZYGIT     │   SHELL  │
└──────────────┴──────────┘
```

**claude** (7 panes):
```
┌──────────────┬──────────┐
│              │  MONITOR │
│   CLAUDE     ├──────────┤
│              │  FILES   │
│              ├──────────┤
│              │  REMOTE  │
├──────────────┼─────┬────┤
│   LAZYGIT    │SHELL│LOG │
└──────────────┴─────┴────┘
```

**monitor** (4 panes):
```
┌──────────────┬──────────┐
│   MONITOR    │  SHELL   │
├──────────────┼──────────┤
│   LAZYGIT    │  LOGS    │
└──────────────┴──────────┘
```

### Themes | 테마

5 built-in themes: `default`, `dracula`, `nord`, `catppuccin`, `tokyo-night`.
Session-scoped — multiple workspaces can use different themes simultaneously.

5개 내장 테마. 세션별 적용 — 여러 워크스페이스가 서로 다른 테마를 동시에 사용 가능.

### Tool Fallback Chains | 도구 폴백 체인

Missing optional tools are handled gracefully.
선택적 도구가 없으면 대체 도구가 자동으로 사용됩니다.

| Pane | Primary | Fallback | 폴백 |
|------|---------|----------|------|
| MONITOR | btop | htop → top | 순차적 대체 |
| GIT | lazygit | git status | git 상태 표시 |
| FILES | yazi | eza → tree → ls | 순차적 대체 |
| CLAUDE | claude | install message | 설치 안내 |

---

## Profiles | 프로필

Profiles use YAML inheritance. `full` extends `minimal`.

프로필은 YAML 상속을 사용합니다. `full`은 `minimal`을 확장합니다.

| Profile | Modules | Packages | Use Case | 사용 사례 |
|---------|---------|----------|----------|-----------|
| **minimal** | 5 | 15 | Lightweight dev setup | 경량 개발 환경 |
| **full** | 12 | 26+ | Complete workstation | 완전한 워크스테이션 |
| **server** | 8 | 19 | GPU/DGX server | GPU/DGX 서버 환경 |

**server** profile: Extends `minimal` + tmux, ai-tools, conda. Disables workspace, fonts, gpg, secrets.
Auto-suggested when NVIDIA GPU or CUDA is detected.

서버 프로필: `minimal` 확장 + tmux, ai-tools, conda. workspace, fonts, gpg, secrets 비활성화.
NVIDIA GPU 또는 CUDA 감지 시 자동 제안.

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
| `DOTFILES_REPO_DIR` | Dotfiles repo directory | 저장소 경로 |
| `DOTFILES_HOME` | Override home directory | 홈 디렉토리 재정의 |
| `GITHUB_TOKEN` | GitHub API token for `upgrade` | `upgrade` 명령의 GitHub API 토큰 |

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
│   ├── cli/                      # Cobra commands (12 files)
│   │   ├── open.go               # dot open — workspace launcher
│   │   └── workspace_cmds.go     # stop, list, register, unregister, layouts, doctor
│   ├── config/                   # Config struct, loader, detector, state
│   │   └── profiles/             # Embedded YAML profiles (go:embed)
│   ├── exec/                     # Runner (dry-run), Brew wrapper
│   ├── module/                   # 12 module implementations
│   ├── workspace/                # Workspace management
│   │   ├── config.go             # Project config, YAML load/save
│   │   ├── deps.go               # Tool dependency checker
│   │   ├── deploy.go             # Shell script deployer (go:embed)
│   │   └── scripts/              # Embedded shell scripts
│   │       ├── launcher.sh       # Session create/resume
│   │       ├── layouts.sh        # Layout definitions (dev, claude, monitor)
│   │       ├── tools.sh          # Tool launchers with fallback chains
│   │       └── themes.sh         # Theme definitions (5 themes)
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

### Test Pipeline

| Job | Matrix | Description | 설명 |
|-----|--------|-------------|------|
| **unit** | ubuntu-latest, macos-latest | Go unit tests + coverage | 유닛 테스트 + 커버리지 |
| **integration** | ubuntu-{22.04,24.04} × {minimal,full,server} + GPU sim | Docker-based profile tests | Docker 기반 프로필 테스트 |
| **module** | 8 modules × ubuntu-22.04 | Individual module tests | 개별 모듈 테스트 |
| **scenario** | 7 E2E scenarios | dry-run, idempotency, server, upgrade, home-override, workspace | E2E 시나리오 테스트 |

- **Release**: Triggered by `workflow_run` — only after Test succeeds on a `v*` tag. Uses GoReleaser for cross-platform builds (darwin/linux × amd64/arm64).

### Creating a Release | 릴리스 생성

```bash
git tag v0.1.0
```

```bash
git push origin v0.1.0
```

Test workflow runs → on success → Release workflow creates GitHub Release.

---

## GPU Server / DGX Provisioning | GPU 서버 프로비저닝

On a fresh DGX or GPU server — auto-detects NVIDIA GPU + CUDA.
새 DGX 또는 GPU 서버에서 — NVIDIA GPU + CUDA 자동 감지.

```bash
curl -fsSL https://raw.githubusercontent.com/entelecheia/dotfiles-v2/main/scripts/install.sh | bash
```

Auto-selects 'server' profile | 'server' 프로필 자동 선택:

```bash
dotfiles init --yes
```

Apply packages, shell, git, ssh, terminal, tmux, ai-tools, conda:

```bash
dotfiles apply --yes
```

Detection logic | 감지 로직:
- `nvidia-smi` → GPU model detection | GPU 모델 감지
- `/usr/local/cuda` → CUDA home path | CUDA 홈 경로
- `/etc/dgx-release` → DGX identification | DGX 식별

---

## Development | 개발

Build:

```bash
make build
```

Run tests:

```bash
make test
```

Lint:

```bash
make lint
```

Clean build artifacts:

```bash
make clean
```

Install to ~/.local/bin/:

```bash
make install
```

## License

MIT
