# agentmd

A small collection of **agent skills** — reusable, model-agnostic working disciplines and engineering conventions, each written as a single Markdown file an AI coding agent can load on demand.

The goal: capture the rules you'd otherwise repeat in every prompt ("don't delete data without asking", "where does this Go function belong?") once, in a form an agent can trigger automatically when the work matches.

---

## What is a skill?

A skill is a folder containing a `SKILL.md` file. That file has two parts:

1. **Frontmatter** (YAML) — metadata the agent reads to decide *when* to use the skill.
2. **Body** (Markdown) — the actual instructions, conventions, and checklists.

The agent does not read every skill body all the time. It reads the short `description` from the frontmatter, and only pulls the full body into context when the current task matches. This keeps skills cheap to have installed and lets you accumulate many without bloating every prompt.

```
skill-name/
└── SKILL.md          # frontmatter + instructions
    └── references/   # optional: deeper docs loaded only when needed
```

---

## Skills in this repo

| Skill | When it triggers | What it enforces |
|-------|------------------|------------------|
| [`safety-first`](safety-first/SKILL.md) | Writing/reviewing config, env, or credential code; DB migrations; bulk or destructive operations | No fallback default secrets, mandatory startup config validation, human confirmation before destructive ops, backups + dry-runs before migrations, soft-delete over hard-delete, transactional safety |
| [`go-func`](go-func/SKILL.md) | Writing, structuring, or reviewing a Go monolith domain — "add a use case", "write a repository", "where does this function go?" | The three function types (Pure Logic / Side Effect / Orchestration), Ports & Adapters layout, append-only repos, publish-only events — selectable via a **Simple** or **Advanced** profile |

### `safety-first`

A zero-data-loss discipline for infrastructure and configuration work. It refuses silent fallbacks for secrets, requires required env vars to be validated at startup (fail-fast, not default-and-continue), and demands explicit human approval — with impact, affected resources, and a rollback plan — before anything destructive (drops, truncates, mass updates, teardown). It prefers additive migrations and soft-deletes, and includes ORM-specific guardrails (e.g. partial-update vs overwrite, context propagation, no external calls inside transactions).

Its frontmatter runs it as a **deny-edit subagent** (`mode: subagent`, `permission.edit: deny`) so it can review and advise without modifying files.

### `go-func`

A Ports & Adapters (hexagonal) convention for Go monoliths, built around one rule: **every function is exactly one of three types** — Pure Logic (no I/O), Side Effect (DB/network/time/random, in adapters), or Orchestration (composes the two). That discipline is constant; the surrounding ceremony scales via a **profile**:

- **Simple** — flat package per domain (files, not folders), services may call repositories directly, ordinary CRUD, interfaces only where you need a test/swap seam. For small or early monoliths.
- **Advanced** — full `domain/port/application/adapter` split per domain, publish-only domain events, append-only repositories (no Update/Delete), compile-time interface checks, isolated bounded contexts for external integrations. For large, multi-domain systems.

You can start Simple and graduate one domain at a time — promotion is moving files into folders, not a rewrite. The skill is **storage-agnostic** (no DB or library hard-coded).

---

## Installing & using

Skills work with any agent that can read Markdown instructions. For [Claude Code](https://claude.com/claude-code), install a skill by placing its folder in a skills directory:

| Scope | Location | Effect |
|-------|----------|--------|
| **Personal** (all your projects) | `~/.claude/skills/<name>/` | Available in every session |
| **Project** (shared with a repo) | `<repo>/.claude/skills/<name>/` | Available to anyone working in that repo |

**macOS / Linux (bash):**

```bash
# Personal install (copy)
cp -r go-func      ~/.claude/skills/go-func
cp -r safety-first ~/.claude/skills/safety-first

# …or symlink so they track this repo
ln -s "$(pwd)/go-func"      ~/.claude/skills/go-func
ln -s "$(pwd)/safety-first" ~/.claude/skills/safety-first
```

**Windows (PowerShell):**

```powershell
# Personal install (copy)
Copy-Item -Recurse go-func      "$env:USERPROFILE\.claude\skills\go-func"
Copy-Item -Recurse safety-first "$env:USERPROFILE\.claude\skills\safety-first"

# …or symlink so they track this repo
New-Item -ItemType SymbolicLink -Path "$env:USERPROFILE\.claude\skills\go-func"      -Target "$pwd\go-func"
New-Item -ItemType SymbolicLink -Path "$env:USERPROFILE\.claude\skills\safety-first" -Target "$pwd\safety-first"
```

To install for a single repo instead of all your projects, swap `~/.claude/skills/` (or `$env:USERPROFILE\.claude\skills\`) for `<repo>/.claude/skills/`.

Once installed, the agent triggers the skill automatically when a task matches its `description`. You don't have to name it — though you can invoke it explicitly (e.g. `/go-func` in Claude Code) to force it.

For agents that aren't skill-aware, just point them at the `SKILL.md` file directly, or paste its body into your prompt / project instructions.

---

## Authoring a new skill

1. Create a folder named after the skill (kebab-case).
2. Add a `SKILL.md` with frontmatter + body.
3. Add a row to the table above.

### Frontmatter reference

```yaml
---
name: my-skill                 # required — kebab-case, matches the folder
description: >                 # required — what it does AND when to trigger.
  One or two sentences. The agent matches the task against this, so name
  concrete triggers ("write a repository", "before a migration").
license: MIT                   # optional
mode: subagent                 # optional — run as an isolated subagent
model: anthropic/claude-...    # optional — pin a model for this skill
permission:                    # optional — restrict what the skill may do
  edit: deny                   #   e.g. a review-only skill that never writes
  bash: allow
---
```

The `description` is the most important field — it's the only part the agent sees before deciding to load the skill. Write it for *recall*: lead with the domain, then list concrete situations that should trigger it.

### Body guidance

- Lead with the single most important rule, then layer detail.
- Prefer tables and checklists over prose — they're easier for an agent to apply.
- Keep examples generic unless the skill is genuinely project-specific.
- Push rarely-needed depth into `references/*.md` and mention when to read them, so the main body stays lean.

---

## Repository layout

```
agentmd/
├── README.md            — this index
├── safety-first/
│   └── SKILL.md         — infra & config safety discipline
└── go-func/
    └── SKILL.md         — Go hexagonal convention (Simple / Advanced)
```

---

## License

MIT, unless a skill's own frontmatter states otherwise.
