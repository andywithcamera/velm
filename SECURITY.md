# Security Policy

> *Yes, we know. Someone's going to find a hole in this thing. They always do. The difference between us and the amateurs is what happens next.*

---

## Supported Versions

Not all versions are created equal. Some are actively defended. Some are museum pieces. Choose accordingly.

| Version | Supported          | Notes |
| ------- | ------------------ | ----- |
| 0.x.x (alpha) | ✅ Yes        | Active development. Expect chaos. Report anyway. |

We are pre-1.0. Everything is in flux, which means the attack surface is also in flux. Security reports are still taken seriously — arguably *more* so, because now is the time to burn things down cleanly before the stable release cements our mistakes into history.

---

## Reporting a Vulnerability

Found something? Good. That means you were paying attention, which puts you ahead of approximately 80% of the internet.

Here's how this works:

### Where to Report

Do **not** open a public GitHub issue. If you do, you've just handed a gift-wrapped exploit to every script kiddie with a RSS reader. Don't be that person.

Instead, report vulnerabilities via one of the following channels:

- **GitHub Private Security Advisory**: Navigate to the `Security` tab → `Advisories` → `Report a vulnerability`. GitHub encrypts it. We receive it. 

### What to Include

A useful report contains:

- Affected version(s)
- Description of the vulnerability
- Steps to reproduce — precise ones, not "it breaks sometimes"
- Potential impact assessment
- Proof-of-concept code if applicable (responsible disclosure, not a live weapon)

A useless report says *"i found a bug its bad"*. We'll ask you to try again.

### What Happens Next

We take this stuff seriously. Here's the timeline you can expect:

| Milestone | Target Timeframe |
|-----------|-----------------|
| Acknowledgement of receipt | Within **48 hours** |
| Initial triage and severity assessment | Within **5 business days** |
| Status update (accepted/declined/investigating) | Within **10 business days** |
| Patch release (if accepted) | Depends on severity — critical issues get expedited |
| Public disclosure | Coordinated with reporter, typically 90 days post-patch |

### If the Vulnerability is Accepted

- You'll be credited in the release notes (unless you prefer anonymity — your call)
- We'll coordinate disclosure timing with you
- For significant finds, we have a bug bounty program — details provided during triage
- We will not threaten you, sue you, or do anything else legally questionable. We're not that kind of operation.

### If the Vulnerability is Declined

- You'll get a clear explanation of why
- Common reasons: already known, out of scope, not reproducible, works as intended
- You're welcome to disagree. Present new evidence and we'll look again.

### Scope

**In scope:**
- Authentication and authorization flaws
- Remote code execution
- SQL injection / data exfiltration
- Privilege escalation
- Cryptographic weaknesses

**Out of scope:**
- Denial of service via resource exhaustion (we know, everyone knows)
- Social engineering attacks against our team
- Vulnerabilities in unsupported versions (see table above)
- "I can see my own data" — yes, that's the point

---

## Our Commitment

We will not retaliate against good-faith security researchers. We will respond. We will fix what needs fixing. And we will do it without a lot of drama.

The security of this project is a shared responsibility. You found it. We'll patch it. Together we make this marginally less terrible for everyone.

---

*Last updated: 2026*
