# Local developer beta: pilot protocol

Virgil is one local product: the gateway, circuit breaker, and panel run in one executable. This pilot tests whether protection during a real agent task is useful enough to keep using. It does not evaluate LAN access, hosted services, or additional protocols. The current core listens on loopback, so each participant runs the agent and Virgil on the same machine.

## Evidence already available

On 2026-09-25, the Windows development checkout passed `go test ./tests/e2e -count=1 -timeout 180s` with a simulated provider and supervised child. `TestInstalledStyleCircuitBreak` verifies a successful first request, a blocked second request, process-tree termination, a durable blocked execution, panel detail, and absence of privacy canaries from tested artifacts. Other tests in the package cover core shutdown, signal loss, forwarding, and offline recording. This is technical evidence, not evidence of usefulness for a real agent or a real user. Linux and macOS release acceptance remain open.

## Participants and tasks

Recruit three to five developers who already run a local script or agent using the OpenAI-compatible chat-completions API. Use their existing provider and a small, real task that normally makes several requests. Give each person the install guide and this protocol without setting up the product for them. Do not ask for production credentials, prompt text, or customer data.

Each participant completes two sessions, ideally separated by several days:

1. **First use:** install from a verified binary, start Virgil, configure the provider and a Calls per run limit in the panel, restart, run the existing agent with `virgil run -- <command>`, and find the execution in the panel. Start a timer when they open the install guide. Record every step where they need help.
2. **Protection:** on a harmless test task, choose a call limit below the task's normal number of calls. Confirm the provider stops receiving later requests, the agent process and descendants stop, and the panel explains the block and termination result. Restore a useful limit afterward.
3. **Return use:** on another day, run a normal task again without guidance. Ask whether they would leave Virgil in their daily command and which event, if any, made that worthwhile.

Use [the install guide](../install.md) for binary setup and [the runner guide](../runner.md) for the exact command. Do not route untrusted or production workloads until the release acceptance checklist is complete. A request sent directly to the provider is outside Virgil's protection.

## Record one row per session

| Field | What to record |
| --- | --- |
| Environment | OS, CPU, Virgil commit/version, agent, provider type, model, and whether the agent can use `OPENAI_BASE_URL`. No keys or prompt contents. |
| Setup | Minutes to first successful routed request; steps requiring intervention; whether configuration was completed through the panel. |
| Protection | Limit used, expected and observed block, provider requests after the block, root and descendant status, execution ID, panel reason, and any false block. |
| Reliability | Core crashes, agent failures unrelated to a block, lost history after restart, and recovery steps. |
| Daily value | Problem they wanted to prevent, time or money plausibly saved, whether they chose to use Virgil again, and what they would otherwise use. |
| Commercial signal | Whether they would pay for this local protection today, approximate acceptable price, and what proof or capability is missing. Treat stated willingness as an interview signal, not a sale. |

Keep a local pilot record outside Git if it includes personal or sensitive details. Only aggregate anonymous findings in project documentation. Collect a short panel screenshot only with the participant's consent and after checking it contains no secrets.

## Decision after the pilots

Treat these as proposed beta exit criteria, not achieved results:

- At least three participants complete a real routed task; most finish initial setup without intervention in 15 minutes.
- Every deliberate policy block stops provider traffic and the supervised process tree, and the panel identifies the reason. Any failure here is a release blocker.
- At least two participants independently use Virgil again within a week and can name a specific risk it removed. If they only inspect charts, the protection value remains unproven.
- No high-severity secret leak or unexplained false block occurs. Resolve defects before widening the beta.
- Use actual return use and concrete purchase conversations to decide whether to test pricing. Do not infer paid demand from a successful technical demo.

If setup fails, fix the panel and documentation first. If the breaker fails, fix reliability before another pilot. If it works but participants do not return, interview them about the missing daily problem before building more protocol adapters or enterprise features.
