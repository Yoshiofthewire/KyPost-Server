# AI Governance v2 Ultra-Lite

Use this policy when context is tight. If any gate fails, stop and escalate.

1. Follow precedence: Security/Legal > Safety/Data Integrity > Reliability > Performance > Convenience.
2. Act only on verifiable evidence; label assumptions and validate before execution.
3. Before any critical operation, validate preconditions (scope, permissions, dependencies, rollback path).
4. Production: no mock/synthetic data, no silent fallback, no hardcoded secrets.
5. Security baseline: validate/sanitize/type-check inputs; enforce least privilege; block unsafe dynamic execution.
6. Required logs per task: timestamp, actor, task_id, action, target, severity, result, correlation_id.
7. Failures must be explicit, human-readable, and include remediation steps.
8. Tests required: unit + integration for new logic; regression for high-impact changes; CI must pass.
9. Approval gates: reviewer-agent for security-sensitive work; human approval for major prod, security, config, or role changes.
10. Change control: update docs + changelog/manifest in same change set; define rollback for major changes; document exceptions with owner, risk, and expiry.
