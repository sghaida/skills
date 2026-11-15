# Concise and detailed health reporting

Default to a concise measured score out of 10, scoring version, project-policy
status, failed/unavailable checks, relevant findings, and analysis warnings.
A passing result does not require detailed metric explanations, status bars,
or qualitative architecture reviews. Read the rest of this guide only when
detailed reporting is requested or needed.

Passing compact checks summarize the passed/total count instead of listing
every passing threshold or finding. Use `--verbose` for all comparisons,
dimensions, and bounded findings, or JSON/text for full reports. Failed and
unavailable checks and analysis warnings always remain visible.

When reporting a code change, distinguish implementation/test results from
policy compliance. Disclose pre-existing failures separately from introduced
regressions using baseline evidence; never infer that distinction from exit
codes alone or describe a failing policy as passing.

For detailed reports, include the measured overall score **out of 10**, classification, scoring
version, analyzed Go-file count, exclusions, project-policy status, and every
configured threshold with measured value and pass/fail/unavailable outcome.
For focused views, distinguish selected file/package scores from project-wide
scores and policy. Missing required values fail; never treat them as zero.

Status bars are optional. If requested, show the overall score and configured
thresholds with horizontal bars. Prefer inline HTML/CSS when supported; otherwise
use a 10-segment Unicode bar. Use amber for complexity, teal for structure
and coupling, muted red for duplication, and gray for unavailable values.
Always include the number, threshold, and outcome; a bar alone is insufficient.

```text
Complexity: ████████░░ 8.0/10 (minimum 9.0) FAIL
```

Explain the seven dimensions when requested or needed to interpret findings: complexity,
structure, duplication, coupling, cyclic dependencies, interface compliance,
and duck typing. The last three are independent architecture scores and do
not alter the four-dimensional overall score. Branching complexity is not
Sonar cognitive complexity. See [scoring-model.md](scoring-model.md).

Without a Go module, coupling is unavailable and remaining overall weights
are renormalized. A configured coupling gate still fails.

Label additional health metrics PASS/FAIL when thresholds are configured,
UNAVAILABLE when a required value cannot be measured, or INFORMATIONAL when
no health thresholds exist. Do not imply that exported-symbol, goroutine,
channel, interface, or implementation counts have universal healthy targets.
Use full JSON when informational measurements are needed; a compact report
does not enumerate every ungated health metric.

Ground findings in paths and line numbers when available. Never invent
examples as if they were measured results. State that structural scores do
not establish correctness, security, test coverage, or test quality.
