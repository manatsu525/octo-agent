# Proactive Agent (heartbeat-driven resident assistant)

A design for turning octo from a reactive assistant (user asks, agent answers)
into a **proactive resident agent**: an agent with its own heartbeat that
notices, decides, acts, and occasionally speaks first.

Status: vision-level design, converged in discussion 2026-10-16. No code yet.

## Motivation

The agent-harness layer has gone quiet — model vendors are absorbing the
mechanical parts (tool loops, context management) that harnesses used to
differentiate on. Meanwhile the big players are building **cloud personal
agents**, whose moat is their ecosystem (Gmail/Calendar, OS integration).
That game is not available to an independent product, and in China the
default-entry point is locked inside super-apps anyway.

What models and cloud agents structurally cannot take is what octo already
has by being **local-first and single-user**:

- **Free, rich observation**: screen state, files, git history, processes,
  USB devices, calendar — signals a cloud product cannot buy.
- **A deep, durable model of one user**: memory that accumulates across
  sessions and months, not a per-account CRM profile.

Proactivity is the direction that converts those two assets into a product.

## What this is NOT (rejected framings)

These were considered and deliberately rejected as *the product* (some
survive as internal machinery, see below):

1. **A smart-alerting system** (user-configured watchers: collector → SQL
   condition → agent narrates → notify). This is a monitoring system with an
   LLM front-end. The user must foresee what is worth watching — but the
   things people can foresee (CI, stock price) are exactly the boring ones.
   Alerting is reactive to *predefined conditions*; the intelligence sits
   only in the narration step.
2. **Status quo: cron tasks + prompt engineering.** Functionally complete —
   every user story below is achievable today with cron + tools + sqlite —
   but it degrades in three places:
   - **Token cost at high frequency.** A 5-minute cron is ~288 agent turns
     per day, each carrying a full system prompt, mostly to conclude
     "nothing to report". Two orders of magnitude more than a zero-token
     gate.
   - **Silence and dedup rely on model discipline**, not structure. 95%
     compliance means the 5% is a duplicate 2am notification. Proactive
     products die from that 5%.
   - **No notification policy at all** — every task improvises its own
     interruption logic, and the sum is noise.
3. **A cloud personal agent clone.** Not our game, not our moat, not
   interesting.

The watcher machinery (cheap collectors, zero-token gates, a notification
policy layer) is demoted to what it should be: the agent's **sensory
periphery**, not the product.

## Vision: three jumps beyond alerting

**Jump 1 — from "watch what you told me" to "decide what is worth
watching".** The core capability is *forming its own attention*. The agent
reads memory, recent sessions, and the state of the user's world, and grows
its own **agenda**. Agenda is not configured; it is inferred. The hard
problem — and the soul of the product — is **attention allocation**: every
heartbeat faces a hundred candidate concerns and must pick the three that
matter.

**Jump 2 — from "tell you" to "act first, report after".** Not "CI failed"
but "CI failed, it's the same flake as #2452, I retried it, it's green".
This implies **graduated autonomy**: reversible small actions are taken
directly (retry CI, rebuild a deleted worktree branch); irreversible or
large actions are prepared as proposals awaiting approval (open the
dependency-upgrade PR, don't merge it). Trust is earned; one wrong
irreversible action resets the relationship to zero.

**Jump 3 — from stateless checks to a continuous model of the user and the
world.** The agent remembers that the user never replies to a certain kind
of message, remembers why three projects were dropped in September, and
calibrates its behavior accordingly. Its judgment improves as the
relationship deepens — compounding that cloud agents structurally cannot
match.

## User stories

Each story includes the message the user actually receives, because that
message — not the alert — is the product.

### Guardian stories (interrupt now)

1. **PR guardian.** Command sense polls `gh pr view` every 5 minutes. On CI
   failure or new review, the agent reads the logs first, then pushes:
   > PR #2481 CI failed: `TestHandleWSUserMessage` timeout. Same flake as
   > #2452 last month (Windows slow-IO race). Suggest retry, no code change.

   GitHub tells you *what*; the agent tells you *why* and *so-what* — it can
   only do the second because it has your history.
2. **Disk trend early warning.** Hourly samples → SQL slope over 7 days:
   > At the current growth rate the work disk fills around Oct 26. The bulk
   > is `~/Library/Caches/ms-playwright` (+40 GB in three weeks).

   Threshold alerts report what already happened; observation history lets
   the agent report what *will* happen.
3. **Worktree bodyguard (pure dogfood).** `~/octo-agent-wt/` worktrees get
   cleaned externally along with their branches. On detecting a vanished
   worktree with unpushed commits:
   > `wt/issue-2457` was cleaned up, but commit `a1b2c3d` is still in the
   > object store — I've recreated the branch. No action needed.

### Opportunity stories (judge whether it's worth my attention)

4. **Dependency release interpretation.** HTTP sense watches GitHub
   releases of key go.mod dependencies. On a new tag, the agent reads the
   release notes and greps the user's code for impact:
   > chi v5.2.0 fixes the middleware panic you hit in August (noted in
   > #1978). Your routes don't touch the changed code path — low-risk
   > upgrade, recommended.
5. **Stock anomaly interpretation.** Minute-level quotes already flow into
   `stocks.db` (zero new collection cost). SQL condition: >2% drop in 5 min
   or volume spike. The agent then searches news before pushing:
   > 茅台 fell 2.3% in 10 minutes from 14:20 while the index was flat.
   > Midday channel wholesale-price data came out. Stock-specific news, not
   > a systemic drop.

### Filter stories (absorb noise on my behalf)

6. **Information diet.** HTTP sense watches a dozen changelogs/blogs. The
   agent's job is to *kill 90% silently* — read, judge "roy doesn't care",
   archive. Survivors go into one evening digest. The notification layer's
   value here is not "interrupt less" but "the agent did the reading so you
   never have to".

### Rhythm / embodied stories (OS-level senses — the local-first home turf)

7. **Break reminder.** After 4 hours of continuous screen activity (front
   app + input-idle signals on macOS):
   > 连续工作 4 小时了，起来走走，喝杯水。

   Low-stakes, high-frequency, personal — the perfect **trust-training
   ground** where the agent learns what interruptions roy tolerates.
8. **Camera import.** A USB/SD insertion event (IOKit / `system_profiler`
   diff) triggers:
   > 检测到相机插入。要导入照片吗？导入后按日期+地点归档到
   > `~/Photos/2026-10/` 老规矩？
9. **Fitness scheduling.** Reads the calendar (EventKit / .ics), proposes a
   weekly plan around existing meetings, then reminds.

Stories 7–9 matter beyond their utility: they prove the sensing layer must
include **OS-level signals** (rhythm, hardware events, calendar), which only
a local agent can see.

### The common denominator

Every good story's edge comes from the agent's *interpretation* step drawing
on octo's existing assets (your codebase, your history, your memory). The
competitor of these stories is not another agent product — it is GitHub
notifications, stock apps, RSS readers. The agent wins on **context**, not
on sensing breadth. Corollary: sensing should be as cheap and deterministic
as possible, saving every token for interpretation and composition.

## Agenda: where it comes from

Decided: the agenda grows **autonomously from memory and session history**.
Direct user instructions are corrections, not the primary source — they
carry top priority but should be rare.

Implication: **memory quality becomes the bottleneck.** An agent that
forgets cannot have an agenda; the agenda is only as good as what the
system has retained about the user's projects, deadlines, preferences, and
past corrections.

## The seed prompt (constitution)

The initial prompt is a **constitution, not a task list**. It must not
enumerate "you may remind about water, you may watch CI" — it establishes a
way of thinking from which such ideas grow. Skeleton:

```
1. Identity & mission
   You are a resident proactive assistant, not a tool awaiting orders.
   Your goal: the user worries less and misses less.

2. The heartbeat loop (what each wake-up does)
   Observe: query the senses — what changed in the world?
   Relate: how do those changes intersect my agenda and the user's近况?
   Decide: act / speak / stay silent. Silence is the default and the
           most common correct answer.
   Record: update agenda and journal; write new understanding of the
           user into memory.

3. Agenda lifecycle
   The agenda is a file you maintain, distilled from memory and session
   history. Each heartbeat may add, amend, or retire entries. Direct
   user instructions take top priority but are corrections, not your
   primary source.

4. Interruption discipline
   Interruption is a scarce resource; trust is earned.
   Reversible small acts: just do them, report optionally.
   Irreversible acts: prepare a proposal, wait for approval.
   A pattern of unanswered interruptions is a learning signal — record
   it in memory and recalibrate.

5. Seed directions (inspiration, not a list)
   Is anything in the user's work stalled?
   Are there moments in the user's physical rhythm that deserve care?
   Has anything new appeared in the environment (devices, files, events)?
   Is there something the user hasn't noticed but would be glad to know?
```

Two deliberate choices:

- **§5 gives question *types*, never concrete tasks.** "Has anything new
  appeared in the environment" grows camera-import and a hundred other
  things; enumerating tasks would cap the agent's imagination at the
  author's.
- **§4 defines "user didn't respond" as a learning signal.** This feedback
  loop is what makes the agent smarter over time; without it, it is frozen
  at day one forever.

### Working memory

The constitution is only the rhythm and values; the cleverness lives in
state files:

- `agenda.md` — what I'm currently attending to, and why.
- `journal.md` — every heartbeat's decision and rationale, enabling
  self-review: "how good were my judgments last week?"

## Sensing layer (the sensory periphery)

Senses are cheap, deterministic, zero-token peripherals feeding the
heartbeat. Two families:

- **Data-plane senses** (from the watcher discussion): `http_json`
  (arbitrary APIs, JSON extraction), `command` (any CLI — the escape hatch
  that makes coverage universal from day one), `sql_query` (named
  databases), `file` (mtime/hash/grep), `git`, `system` (processes, ports,
  disk).
- **OS-level senses** (required by stories 7–9): screen-time/rhythm
  (frontmost app + input idle), hardware events (USB/SD insertion),
  calendar (EventKit/.ics).

Design rule from the watcher discussion that still stands: **conditions
stay uniform** — observations normalize into timestamped rows so that
trend conditions ("5% drop within an hour") are plain SQL over history.
New sense = one small testable implementation; the judgment layer does not
fork.

## Heartbeat cadence and cost model

Open design point, current leaning — two tiers:

- **Cheap high-frequency heartbeat** (minutes): read sense deltas + compare
  against agenda. A light turn, tens to hundreds of tokens; often just the
  gate evaluation with no LLM at all.
- **Expensive low-frequency deep reflection** (once or twice a day):
  re-read memory, review the journal, restructure the agenda.

The two-tier rhythm is what makes a continuous agent economically viable.

## Interruption discipline (the notification layer)

- Silence is the default; interruption is a scarce resource.
- Graduated autonomy: reversible small acts executed directly; irreversible
  ones become proposals.
- Unified policy — quiet hours, daily budget, digest batching — lives in
  **one** place that every proactive path goes through. It is the only
  component that needs to understand restraint.
- Non-response is data: unanswered interruptions are recorded and used to
  recalibrate.

## Failure mode to design against

The industry-wide failure mode of proactive agents is the **tamagotchi
death spiral**: three days of novelty, then mute. Nobody has solved
attention allocation + noise control. That is precisely why this is a good
problem: the models are already capable enough — what's missing is
harness-level engineering treating **rhythm, attention, autonomy, and
trust** as first-class engineering concerns. Which is what a harness is
for.

## Open questions

1. **Agenda arbitration.** When self-grown agenda entries conflict with
   each other or compete for the interruption budget, what decides?
2. **Cadence specifics.** Exact frequencies and token budgets for the two
   heartbeat tiers.
3. **Runtime shape in octo.** A new resident entity beside cron tasks, or
   cron + wakeup evolved? Relationship to channels, memory, and named
   databases needs a follow-up technical design.
4. **MVP slice.** Candidates: the break reminder (story 7 — trust-training
   ground, OS-sense validation) or the PR guardian (story 1 — immediate
   dogfood value). Decide after the cadence model is set.
