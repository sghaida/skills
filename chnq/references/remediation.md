# Focused remediation and optional deep reviews

Use this guidance when remediation or qualitative review is requested, or
when a relevant finding requires deeper investigation. Passing checks do not
require architectural, god-object, or mixed-responsibility reviews.

Prioritize measured findings in hotspots and low-structure packages. Read
only the relevant source regions and necessary dependencies, then explain
evidence before suggesting focused extractions.

During a god-object review, inspect structs and methods for unrelated responsibilities,
a broad state surface, or coordination of unrelated I/O, parsing, business,
persistence, and reporting concerns. Report the type, file, line, observed
responsibilities, and supporting evidence. Line counts, method counts, and
scores alone do not establish a god object. These are qualitative review
observations, not numeric analyzer findings.

During a mixed-responsibility review, inspect relevant files and packages. Identify each
observed concern and evidence, and suggest a focused file/package split where
appropriate. Do not equate every low score with a responsibility problem.

Report existing policy failures without automatically expanding the requested
work. Obtain approval before remediating unrelated existing violations.
Use before/after measurements and relevant findings to distinguish existing
failures from introduced regressions; unchanged exit codes alone are not proof.
Fix introduced regressions within scope or report them as unresolved blockers.

Batch related fixes and rerun the complete-project check after each cycle and
after any Go source changes; never substitute a file/package score for the
full gate. Run normal tests and validation separately from read-only analysis.
If authorized health remediation cannot meet its agreed targets safely, report
the blockers rather than claiming that remediation succeeded. Unrelated work
may be complete with disclosed pre-existing failures, but policy remains
failing. Intentional threshold changes require explicit user approval.

For detailed deterministic explanations use `--explain-hotspots`; package
aggregation uses `--hotspots-scope package`. This is optional output, not a
replacement for grounded evidence when a qualitative review is performed.

When recursion checking is enabled, detected direct recursion needs a
non-empty `// chnq:recursion <reason>` doc-comment directive. Review why
recursion is preferable and how excessive depth is handled. A directive
suppresses only the documentation warning, not complexity. Both tail and
non-tail recursion count; Go guarantees no tail-call optimization. Presence,
base cases, and comments do not prove safety or termination. Do not invent
`max_recursion_depth` or automatically rewrite recursion. See the exact
detection limits in [project-config.md](project-config.md).
