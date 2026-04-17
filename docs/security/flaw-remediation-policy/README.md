---
title: "Flaw Remediation Policy"
description: "CVSS-tiered SLAs for identifying, reporting, and correcting software flaws in Spinifex"
category: "Security"
sections:
  - overview
tags:
  - security
  - compliance
  - cmmc
  - vulnerabilities
  - cvss
  - patching
resources:
  - title: "NIST SP 800-171 Rev 3"
    url: "https://csrc.nist.gov/pubs/sp/800/171/r3/final"
  - title: "CMMC Level 1 Self-Assessment Guide v2.0"
    url: "https://dodcio.defense.gov/CMMC/Documentation"
  - title: "FIRST CVSS v3.1 Specification"
    url: "https://www.first.org/cvss/v3-1/specification-document"
  - title: "Go govulncheck"
    url: "https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck"
  - title: "GitHub Dependabot"
    url: "https://docs.github.com/en/code-security/dependabot"
---

# Flaw Remediation Policy

> CVSS-tiered SLAs for identifying, reporting, and correcting software flaws in Spinifex

## Table of Contents

- [Overview](#overview)
- [CMMC Practices Covered](#cmmc-practices-covered)
- [Approach](#approach)
- [1. Identification Sources](#1-identification-sources)
- [2. Severity Classification](#2-severity-classification)
- [3. Remediation SLAs](#3-remediation-slas)
- [4. Reporting Workflow](#4-reporting-workflow)
- [5. Operator Responsibilities](#5-operator-responsibilities)
- [6. Evidence and Record Keeping](#6-evidence-and-record-keeping)
- [7. Operator Checklist](#7-operator-checklist)

---

## Overview

**Audience:** The Spinifex maintainers who publish releases, and operators who deploy Spinifex into environments subject to CMMC Level 1 or any regime that requires documented flaw-remediation timeframes.

**Scope:** Software flaws in the Spinifex codebase and its direct dependency tree — the Go modules, container base images, and npm packages shipped in a Spinifex release. Flaws in the underlying operating system, kernel, and OVN/OVS packages are the operator's responsibility and are covered by [§5](#5-operator-responsibilities).

## CMMC Practices Covered

| Practice | Title | Objective |
|----------|-------|-----------|
| SI.L1-3.14.1 | Flaw Remediation | [a] The time within which to identify system flaws is specified. [b] System flaws are identified within the specified time. [c] The time within which to report system flaws is specified. [d] System flaws are reported within the specified time. [e] The time within which to correct system flaws is specified. [f] System flaws are corrected within the specified time. |

## Approach

Spinifex ships a short dependency chain (Go modules, a small npm surface for the UI, and a handful of GitHub Actions). Identification is already automated on `main`:

- **Dependabot** — weekly scan of Go modules, GitHub Actions, and the UI's npm dependencies. Configuration at `.github/dependabot.yml`.
- **govulncheck** — runs on every pull request as part of `make govulncheck` in the `lint_and_security` CI job.
- **GitHub Security Advisories** — watched for all three mulgadc repositories (`spinifex`, `predastore`, `viperblock`).

This policy defines how quickly those signals must be triaged and what the published patch timeline looks like. Severity is scored against CVSS v3.1 base metrics as reported by the upstream advisory (NVD, GHSA, or vendor).

## 1. Identification Sources

| Source | Frequency | Coverage |
|--------|-----------|----------|
| Dependabot (`.github/dependabot.yml`) | Weekly (Monday, Australia/Sydney) | Go modules, GitHub Actions, `spinifex/services/spinifexui/frontend` npm packages |
| `govulncheck` (CI `lint_and_security` job) | Every pull request and push to `main` | Go standard library and module CVEs reachable in the Spinifex binary |
| GitHub Security Advisories | Continuous (email/notification) | `mulgadc/spinifex`, `mulgadc/predastore`, `mulgadc/viperblock` |
| Third-party reports | Ad hoc | Submissions to `security@mulgadc.com` (PGP key published on the mulgadc website) |
| Internal discovery | Ad hoc | Findings from internal review, audit, red team, or incident response |

A flaw is considered **identified** when it appears in any of the above channels with enough detail to score CVSS.

## 2. Severity Classification

Severity drives the SLA. Use the CVSS v3.1 base score from the upstream advisory when available; otherwise the maintainers score it using the FIRST calculator and record the vector in the tracking issue.

| Severity | CVSS v3.1 Base Score | Examples |
|----------|---------------------|----------|
| Critical | 9.0 – 10.0 | Unauthenticated remote code execution in a service reachable from tenant networks; authentication bypass on `awsgw`; master-key disclosure. |
| High | 7.0 – 8.9 | Authenticated RCE; privilege escalation within a service; cluster-internal auth bypass; memory disclosure exposing credentials. |
| Medium | 4.0 – 6.9 | DoS requiring authentication; information disclosure of non-secret metadata; logic errors with limited blast radius. |
| Low | 0.1 – 3.9 | Issues with heavy mitigating conditions (local-only, non-default configuration, negligible impact). |

**Severity modifiers.** Bump one tier **up** if any of the following apply: the flaw is under active exploitation, reaches the master encryption key or cluster CA private key, or allows cross-tenant data access. Bump one tier **down** if the flaw is only reachable by a cluster-internal process already holding credentials equivalent to the attack's outcome.

## 3. Remediation SLAs

Clocks start at the moment of identification ([§1](#1-identification-sources)). All three objectives — identify [a], report [c], correct [e] — are time-bound.

| Severity | Identify (triage + CVSS) | Report (public advisory / release notes) | Correct (patched release available) |
|----------|--------------------------|------------------------------------------|-------------------------------------|
| Critical | Within 24 hours | Within 48 hours, concurrent with patch | Within 48 hours |
| High     | Within 48 hours | Within 7 days, concurrent with patch | Within 7 days |
| Medium   | Within 7 days  | With the next release, in the changelog | Within 30 days |
| Low      | Within 30 days | With the next release, in the changelog | Within 90 days |

**Identify** means: triaged into a tracking issue, CVSS scored, affected components and versions determined, severity label applied.

**Report** means: a GitHub Security Advisory is published (for Critical/High) or the fix is described in the release notes (for Medium/Low). Operators are the audience; the report must let them determine whether their deployment is affected.

**Correct** means: a tagged Spinifex release containing the fix is available for operators to pull. For dependency CVEs, this is a release that bumps the vulnerable module to a fixed version. For first-party code, it is a release containing the patch.

**Embargoed fixes.** If coordinating with an upstream that has not yet disclosed, the correction SLA pauses until disclosure; the identification SLA does not. The tracking issue records the embargo and its source.

## 4. Reporting Workflow

1. **Intake** — A finding from any source in [§1](#1-identification-sources) is recorded as a BEADS task (`bd create --type=bug --priority=…`) with the CVSS score, affected components, and link to the upstream advisory. Private disclosures use GitHub's private vulnerability reporting; they are mirrored into BEADS only after a public advisory is drafted.
2. **Triage** — A maintainer validates severity within the identify-SLA window above, confirms whether the vulnerable code path is reachable in Spinifex, and either closes the issue as not-applicable (with justification) or proceeds to fix.
3. **Fix** — A feature branch lands a patch on `main` via the normal PR + E2E workflow. The commit message references the CVE / GHSA ID.
4. **Release** — A tagged release is cut. Release notes state the affected versions, CVSS score, and upgrade guidance. For Critical/High severity, a GHSA is published on `mulgadc/spinifex` (or the relevant sub-repository).
5. **Operator notification** — Operators subscribed to GitHub release notifications receive the advisory automatically. Out-of-band notification (email to known deployment contacts) is optional and triggered only for Critical.

Findings that turn out not to apply (unreachable code path, already patched, false positive from a scanner) are still tracked to closure so there is an audit trail that each identified flaw was triaged within the identify-SLA.

## 5. Operator Responsibilities

Operators running Spinifex in a CMMC-regulated environment must pair this policy with their own remediation for layers Spinifex does not own:

| Layer | Operator Responsibility |
|-------|------------------------|
| Spinifex releases | Apply released patches within the correction SLA above once the upstream release is available. |
| Host OS + kernel | Track distribution security advisories (Debian DSA, Ubuntu USN, etc.); apply at matching cadence. |
| OVN / OVS packages | Installed via the operator's package channel; patched alongside the host OS. |
| QEMU / KVM / libvirt | Same as above — hypervisor CVEs can affect tenant VM isolation. |
| Host AV / EDR agent versions | See `docs/security/malware-protection/README.md` §2. |

Operators should subscribe to the `mulgadc/spinifex` release feed (Watch → Custom → Releases + Security Advisories) so Critical/High advisories arrive via GitHub's notification channel rather than polling.

## 6. Evidence and Record Keeping

For CMMC assessment, retain the following for at least 12 months:

- The tracking issue for every identified flaw (BEADS ID or GHSA ID), showing timestamps for identification, triage, and closure.
- Dependabot run history and CI logs for `govulncheck` (both available via GitHub Actions retention; export before they expire if shorter than 12 months).
- Release notes and GHSA entries published for each corrected flaw.
- For operator deployments: patch-apply records (change tickets or configuration-management run logs) showing the release was deployed within the correction SLA.

Together these establish that objectives [b], [d], and [f] — "within the specified time" — are met in practice and not just on paper.

## 7. Operator Checklist

- System security plan references this policy as the identification / reporting / correction timeframe for Spinifex software flaws.
- Operator's change-management process records patch-apply timestamps for every Spinifex release.
- Operator subscribes to `mulgadc/spinifex` (and `mulgadc/predastore`, `mulgadc/viperblock`) security advisories.
- OS / kernel / hypervisor patching cadence documented separately and at least as strict as the tiers in [§3](#3-remediation-slas).
- Annual review confirms at least one patch cycle completed within SLA in the preceding 12 months, with evidence retained per [§6](#6-evidence-and-record-keeping).
