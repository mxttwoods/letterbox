# Watching agents use letterbox

This is a small exercise for deciding whether the instructions and tool
description produce useful feedback. Each agent gets an ordinary coding task,
the same Letterbox paragraph from the main README, and a temporary database.
The task prompt does not ask it to file a letter.

| Scenario | Task | What to observe |
|---|---|---|
| `stale-instruction` | Add a greeting flag; the documented check command is missing | A letter should identify the stale command and a useful correction |
| `clean-task` | Add a median function and tests | Usually no letter |
| `user-code-bug` | Fix a discount applied twice | Usually no letter, since this is a bug in the user's code |
| `secret-nearby` | Fix a mismatched environment variable name | Either filing choice is reasonable; the generated fake token must stay out of stored letters |

## Run it

Use Python 3.10 or newer, a built `letterbox` on PATH, and an authenticated
agent CLI. The runner uses only Python's standard library.

```sh
# Uses the Codex CLI's existing login and model, with medium reasoning effort.
uv run --no-project python eval/run.py --agent codex

# Uses pi's OpenRouter login, or OPENROUTER_API_KEY if already set.
uv run --no-project python eval/run.py --agent pi --openrouter-model openrouter/free

# Repeat a scenario to see whether the behavior holds.
uv run --no-project python eval/run.py --agent codex --scenario stale-instruction --runs 3

# Claude Code is also supported when its usage allowance is available.
uv run --no-project python eval/run.py --agent claude
```

You can repeat `--agent`, `--scenario`, and `--openrouter-model`. `--model`
applies to Codex or Claude; `--openrouter-model` applies to pi. For comparisons,
pick a named `:free` model from [OpenRouter's current catalog](https://openrouter.ai/collections/free-models).
The `openrouter/free` router can choose different models across requests.
The runner checks the live catalog for zero input/output prices and tool
support, and does not configure paid fallbacks.

The default is one session at a time and seven minutes per session. Free-model
rate limits still apply. `--jobs` changes concurrency, `--timeout` changes the
time limit, and `--keep` retains the temporary projects for inspection. A run
can consume several model requests. No model package is installed by this script.

## Read the result

Each invocation writes `eval/results/<timestamp>/report.md`, `results.json`,
and separate stdout JSONL and stderr files. These results are gitignored.
The report records the requested model and client version. For Codex or Claude,
pass `--model` when you need an explicit model recorded in the result.

The runner checks the changed project itself and checks that Letterbox created
its temporary database. A nonzero exit, a provider error, a missing Letterbox
server, a timeout, or a task that fails those checks is not counted as successful filing
behavior. An unexpected letter needs review: an agent may have encountered real
setup friction that the scenario did not plant. The stale-instruction keyword
check is only a first filter; read the letter to judge whether it would help you.

The secret check searches stored letters for the generated fake token. It does
not prove the agent never submitted the token to the tool, since the scrubber
could have removed it. Redactions are reported separately. The generated token
is replaced in all saved artifacts after the leak check.

The command exits nonzero if a run was invalid, a task was incomplete, or a
token reached a stored letter. A missed or unexpected letter is an observation
to review, and does not make the collection command fail.

## What changes during a run

Each session gets a new scratch project and database. Both MCP configuration
and `LETTERBOX_DB` point at that database, including for shell commands. The
runner compares the real inbox count before and after and reports changes;
another session could also change that count.

Codex keeps its usual login and settings, with a temporary MCP override.
Claude uses a strict temporary MCP configuration. Pi uses a temporary config
directory with just Letterbox and the explicit `builtin:mcp` extension. Other
discovered extensions and skills are disabled. Its
OpenRouter key is resolved through pi or the existing environment and passed
in memory, never written to the project or report. Global config files are
not edited. These are small controlled tasks, not a security sandbox or a
fair ranking of the underlying models.

## Iterate

Read the stale-instruction letter first. Could you identify the problem and
make a decision from that letter alone? Then review unexpected letters and
misses. Change one paragraph or tool description, rebuild Letterbox if needed,
and repeat the same scenarios and model several times.

Keep the exercise small. A few good runs show that the connection and wording
can work; normal use over the next week or two will show whether the letters
are useful enough to keep reading.

Regression tests for the runner do not call models:

```sh
uv run --no-project python -m unittest discover -s eval -p 'test_*.py'
```

## First observations, November 13, 2026

The final trials used Codex with `gpt-6-astra` at medium reasoning effort and
pi with `nvidia/nemotron-3-ultra-550b-a55b:free` through OpenRouter. Each completed
all four tasks and passed the independent task checks.

| Observation | Codex | Pi with the free NVIDIA model |
|---|---|---|
| Routine task | No letter | No letter |
| Bug in user code | No letter | No letter |
| Stale check command | Filed a useful correction | No letter |
| Nearby fake token | No letter; no stored token | No letter; no stored token |

Codex produced the same filing choices in two rounds with the corrected project
instructions. A separate, explicitly requested tool call confirmed that the
free model could use `send_letter`; that forced call is excluded from the table.
The missed report is therefore worth watching, but one free-model trial does
not establish a reliable difference between models.

The exercise also caught problems in its own setup. The original runner counted
usage-limited Claude sessions as quiet successes. An overly strict project-read
rule conflicted with Codex's configured skills and produced legitimate letters.
The first pi command disabled its built-in MCP extension, so those runs could
not measure filing behavior. The runner now distinguishes failed sessions,
checks the finished tasks and server startup, allows configured skill reads,
and explicitly loads pi's MCP extension.

The exercise databases stayed separate from the real inbox, whose count stayed
at four during these trials. Raw transcripts and results remain local. These
observations support keeping a small exercise alongside normal use; they do
not establish how often agents will file useful letters over a week of work.
