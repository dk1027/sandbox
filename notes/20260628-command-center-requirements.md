# Command Center Requirements

Date: 2026-06-28
Status: draft requirements for incremental build

## Purpose

Build a browser-facing command center at `sre.ryzen.local` for humans to inspect and direct the SRE-agent stack in this repository. It should make the current alert triage and observability workflow easier to use without turning into a generic incident-management platform.

## Goals

- Give an operator one place to see which SRE-agent instances are healthy and what they are doing.
- Show active alerts, agent verdicts, evidence, and remediation recommendations.
- Provide a safe, explicit path for requesting bounded agent actions.
- Make it easy to pivot to Grafana, logs, traces, and Kubernetes object details.
- Keep the system observable: the command center itself must expose metrics and have a dashboard.

## Non-goals

- Arbitrary cluster shell access.
- Full ticketing / incident-command workflow.
- Replacing Grafana, Alertmanager, Loki, Tempo, or the Kubernetes API.
- Unsupervised remediation in v1.

## System overview

```text
Browser
  |
  v
Command Center UI  --->  Command Center API/Backend
  |                         |-- reads agent state / alert history
  |                         |-- requests actions from SRE-agent
  |                         |-- links out to Grafana / logs / traces
  |                         `-- surfaces dependency and auth errors
  |
  +--> sre-agent (/alerts, /metrics)
  +--> mcp-server (/sse, cluster / observability read APIs)
  +--> Grafana / Loki / Tempo / Kubernetes
```

The command center should be the human entry point, but the SRE-agent remains the system of record for alert triage and bounded remediation.

## Primary user actions

1. Inspect the current fleet
   - list configured agents by app / namespace
   - see health, last update time, and recent error state

2. Review an alert
   - open an alert payload or recent alert event
   - see labels, classification, recommendation, confidence, and evidence
   - jump to related logs, traces, and dashboard panels

3. Request an action
   - choose a bounded action recommended by the agent
   - confirm before any state-changing request is sent
   - see success, rejection, or escalation status

4. Investigate failures
   - inspect dependency failures for Prometheus, Loki, Tempo, Kubernetes, or the LLM backend
   - retry once the underlying dependency recovers

## Key screens

### 1. Overview

A landing page that answers: “what is happening right now?”

Must show:
- agent cards for each configured app / namespace pair
- health state: healthy, degraded, unavailable
- open alerts / recent decisions
- recent remediation or escalation activity
- a quick link to the main observability surfaces

### 2. Alert detail

A focused view for one alert or incident thread.

Must show:
- alert labels and metadata
- agent classification and recommendation
- evidence pulled from Prometheus / Loki / Kubernetes
- guardrail reasons if the agent refused to act
- the exact action the user can request next

### 3. Agent detail

A view for one SRE-agent instance.

Must show:
- app / namespace scope
- current remediation mode
- recent webhook outcomes
- recent LLM / remediation errors
- a link to the agent metrics dashboard

### 4. Action composer

A narrow form for explicit, bounded actions.

Must support:
- choosing a target agent
- choosing a safe action type
- showing the expected effect before confirmation
- displaying the final result and audit record

## Interaction pattern

- Read-only by default.
- State-changing operations require explicit user confirmation.
- The UI should treat agent responses as advisory until the user confirms an action.
- Long-running operations should surface a visible in-progress state and final outcome.
- Partial dependency failures should degrade the view, not blank the page.

## Error states

The UI must handle these cases explicitly:

- agent unavailable or not ready
- stale data / last refresh too old
- no matching app / namespace pair
- authorization denied
- action rejected by guardrails
- dependency failure in Prometheus, Loki, Tempo, Kubernetes, or LLM backend
- backend timeout or transient network failure
- malformed agent response

Error UI requirements:
- show the failed dependency and the user-visible consequence
- preserve the alert / action context for retry
- offer a safe fallback path when possible

## Minimal v1 scope

The first usable version should support:

- opening `https://sre.ryzen.local`
- listing the configured SRE-agent instances
- viewing one alert/decision thread end to end
- showing the agent recommendation, evidence, and current status
- sending one explicit bounded action request with confirmation
- showing a visible success / failure result
- linking out to Grafana, logs, traces, and Kubernetes details
- exporting a small audit trail of recent requests and outcomes
- exposing the command center’s own health and metrics

Out of scope for v1:
- multi-user collaboration
- complex incident timelines
- free-form chat with unrestricted tool use
- custom workflows per team
- mobile-specific UX

## Acceptance criteria for v1

- A user can load the command center at `sre.ryzen.local` and see at least one agent and its current health.
- A user can open an alert detail page and understand what the agent saw, what it recommended, and why.
- A user can request one bounded action and see a definitive success, rejection, or escalation outcome.
- The UI must not execute a state-changing request without confirmation.
- Missing observability dependencies must produce a partial-data state, not a hard failure page.
- The command center must emit operational metrics and have a Grafana dashboard so it can be monitored like the rest of the stack.

## Open questions / risks

- AuthN/AuthZ: who is allowed to see alerts and request actions?
- Backend shape: should the UI talk directly to `sre-agent`, or through an intermediary API that also uses `mcp-server`?
- Source of truth: where should action history and audit events live?
- Streaming model: do we need polling, SSE, or websockets for in-progress actions?
- Safety model: what action types are allowed in v1, and which require extra approval?
- Operational risk: a browser-facing control plane increases blast radius if authorization or guardrails are wrong.

## Summary

The command center should be a thin, observable control surface for SRE-agent operations. v1 should prioritize safe readouts, a single explicit action path, and deep links into the existing observability stack rather than trying to replace incident-management tooling.
