# Go Func Skill

Two reusable skills for coding agents. Use **go-func** to structure and review Go monoliths, and **safety-first** to handle credentials, migrations, and data changes safely.

## Choose a skill

| Skill | Use it when | Start here |
| --- | --- | --- |
| `go-func` | Adding a Go use case, repository, handler, or worker, or reviewing a domain package | [Go Func instructions](go-func/SKILL.md) |
| `safety-first` | Loading required configuration or credentials, planning a migration, or reviewing bulk writes or deletes | [Safety-First instructions](safety-first/SKILL.md) |

The skills can work together. For example, use `go-func` to place a new repository and `safety-first` to plan the schema migration it needs.

## Install and try

Clone the repo and copy the full skill folders so the agent can find their references and scripts.

```sh
git clone https://github.com/akkaraponph/go-func-skill.git
cd go-func-skill
```

Choose a destination for your agent and scope. Project paths are relative to the target repository.

| Agent | Personal | Project |
| --- | --- | --- |
| Codex | `~/.agents/skills/` | `.agents/skills/` |
| Claude Code | `~/.claude/skills/` | `.claude/skills/` |

On macOS or Linux, run this from the clone. This example installs both skills for Codex.

```sh
skills_dir="$HOME/.agents/skills"
mkdir -p "$skills_dir"
cp -R go-func safety-first "$skills_dir/"
```

In PowerShell, use the same destination for your chosen agent. This example installs both skills for Claude Code.

```powershell
$skillsDir = Join-Path $HOME ".claude/skills"
New-Item -ItemType Directory -Force -Path $skillsDir | Out-Null
Copy-Item -Recurse -Path go-func,safety-first -Destination $skillsDir
```

Open your target project in the agent and try one of these prompts.

> Use go-func to review this order package. Classify the functions and flag anything that mixes business rules with database access.

> Use safety-first to review this migration. Identify the affected data, the backup and dry-run steps, and the rollback plan.

Both agents can discover the skills from their descriptions. For manual selection, use `$go-func` in Codex or `/go-func` in Claude Code. With any other coding agent, point it at the relevant `SKILL.md`. See the [Codex skill docs](https://learn.chatgpt.com/docs/build-skills#where-codex-loads-local-skills) and [Claude Code skill docs](https://code.claude.com/docs/en/skills#choose-where-skills-load).

## Go Func profiles

Go Func separates **Pure Logic** (deterministic business rules), **Side Effects** (such as storage, network, clock, and randomness), and **Orchestration** (use cases that compose them).

| Profile | Layout | Writes |
| --- | --- | --- |
| **Simple** | One flat package per domain, with interfaces where a test or swap needs them | Services call repositories directly. Ordinary CRUD is supported. |
| **Advanced** | Separate `domain/`, `port/`, `application/`, and `adapter/` packages within each domain | Services publish events. Subscribers persist changes through append-only repositories. |

Start with the [worked Simple `PlaceOrder` example](go-func/references/example.md). For event delivery guarantees, versioning, and how to migrate a domain in stages, read the [Advanced profile](go-func/references/advanced.md).

## Run the architecture checker

Run `archcheck` from the Go module you want to check. It requires Go 1.21 or later and scans packages under `internal/`. Adjust the script path to match your installation.

```sh
go run "$HOME/.agents/skills/go-func/scripts/archcheck.go"
go test -race ./...
```

The checker reports import boundaries, selected I/O and nondeterministic calls in Pure Logic files, and common mutation methods on Advanced repository interfaces. It prints `archcheck: clean` on success and exits nonzero when it finds violations or cannot run. Use the [Go Func review checklist](go-func/SKILL.md#review-checklist) for rules that require judgment.

By default, `internal/platform` is treated as shared infrastructure. Pass `-shared platform,config` after the script path to replace the allowlist with both packages.

## Safety-First and GORM

Safety-First covers startup validation of required configuration, approval before destructive operations, and backups, dry runs, and rollback planning for migrations. For GORM projects, see the [GORM reference](safety-first/references/gorm.md) for partial updates, transaction settings, and soft deletes.

## Contributing

Keep each skill in its own folder with a `SKILL.md`. Match the frontmatter `name` to the folder name and use `description` to say when the agent should load it. Link optional examples and framework guidance from the skill and keep them in `references/`. Add new skills to the table above.

When changing `archcheck`, verify it against a Go module with both a clean layout and a deliberate violation.

## License

MIT, unless a skill's frontmatter specifies otherwise.
