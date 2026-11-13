# letterbox

A suggestion box for coding agents. Agents file short letters about friction
they hit or ideas for working better. You read them every week or so, and can
point an agent at the box to summarize it or build from it.

One Go program, one SQLite file, no background service. Everything stays on
your machine.

The idea comes from Sanity's post
[How to get product feedback from agents](https://www.sanity.io/blog/how-to-get-product-feedback-from-agents).
There the product is their MCP server. Here it is your own agent setup:
instruction files, skills, MCP servers, permissions.

## Install

```
go install github.com/mxttwoods/letterbox/cmd/letterbox@latest
```

That needs Go 1.27 or newer and puts `letterbox` in `~/go/bin`. Make sure that
directory is on your `PATH`, or use the full path in the configs below.

## Connect your agents

Each agent starts `letterbox mcp` itself. `LETTERBOX_AGENT` is the name
recorded on that agent's letters.

Claude Code:

```
claude mcp add --scope user letterbox -e LETTERBOX_AGENT=claude-code -- letterbox mcp
```

Codex, in `~/.codex/config.toml`:

```toml
[mcp_servers.letterbox]
command = "letterbox"
args = ["mcp"]
env = { LETTERBOX_AGENT = "codex" }
```

pi, in `~/.pi/agent/mcp.json` under `mcpServers`:

```json
"letterbox": {
  "command": "letterbox",
  "args": ["mcp"],
  "env": { "LETTERBOX_AGENT": "pi" },
  "exposure": "direct"
}
```

opencode, in `~/.config/opencode/opencode.jsonc` under `mcp`:

```json
"letterbox": {
  "type": "local",
  "command": ["letterbox", "mcp"],
  "environment": { "LETTERBOX_AGENT": "opencode" },
  "codemode": false
}
```

`"exposure": "direct"` and `"codemode": false` matter: without them pi and
opencode don't show the tool to the model up front, and no letters arrive.

Then tell your agents when to use it. One paragraph in your global
instructions file (`CLAUDE.md`, `AGENTS.md`) is enough:

```markdown
## Letterbox
- If the agent setup got in your way (a missing tool or permission, a wrong or
  unclear instruction, a tool error you didn't cause) or you have a concrete
  idea for working better, file it with the `send_letter` tool after finishing
  my request. Don't interrupt the task for it, and filing nothing is fine.
```

## Reading your letters

```
letterbox inbox                 # new letters
letterbox inbox --status open   # everything not done or dismissed
letterbox show 3                # read one in full
letterbox mark 3 --status planned --note "worth doing"
letterbox mark 4 5 --status dismissed
letterbox export --status open  # markdown to hand to an agent
```

Statuses: `new` → `read` → `planned` → `in_progress` → `done`, or `dismissed`.

`letterbox help` lists every command. `letterbox backup <file>` writes a safe
copy; copying the `.db` file by hand can miss recent letters.

## How agents write

`letterbox mcp` offers one tool, `send_letter`. Only the message is required.
The program records which agent sent it and the folder the session was started
in.

Limits: five letters per session per hour, 4,000 characters per message.
Strings that look like API keys, tokens or private keys are redacted before
anything is stored. That is best effort, not a guarantee.

## Letting an agent read the box

Reading is a separate server, `letterbox mcp --reader`, and it is off unless you
turn it on. Letters are text written by other agents, so they stay out of
ordinary sessions.

For one Claude Code session, from a checkout of this repo:

```
claude --mcp-config reader.mcp.json
```

Then ask it to go through the letterbox. To leave it on in Claude Code:

```
claude mcp add --scope user letterbox-reader -e LETTERBOX_AGENT=claude-code -- letterbox mcp --reader
```

A reading agent can list letters, open one, mark it `read`, and add notes. It
can claim a letter as `in_progress` and finish it as `done` only after you have
marked it `planned`. It cannot mark anything `planned` or `dismissed`.

Any agent with a shell can also run `letterbox export`, with no reader set up.

## Settings

| Variable | Meaning |
|---|---|
| `LETTERBOX_AGENT` | Name recorded on letters. Set per agent in its MCP config. |
| `LETTERBOX_PROJECT` | Overrides the recorded folder. |
| `LETTERBOX_DB` | Database path. Default `~/.local/share/letterbox/letterbox.db`. |

## Building from source

```
go test ./...
go build -o ~/.local/bin/letterbox ./cmd/letterbox
```

Layout: `cmd/letterbox` is the command line, `internal/server` the MCP tools,
`internal/store` the database, `internal/clean` the text scrubbing.
