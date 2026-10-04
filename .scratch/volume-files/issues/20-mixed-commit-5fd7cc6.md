# 20 — History: 5fd7cc6 mixes four concerns

**What to build:** nothing. Record the decision about `5fd7cc6 fix: harden deployment and volume
file operations`, which carries the ADR-0011 work, the operation identity, teardown and the lock in
one commit.

**Blocked by:** none

**Status:** wontfix

## Comments

The commit is already on `origin/feat-volume-replan` and both reviews reference it; rewriting
history would invalidate the review trail and force a re-review for no behavioural gain. Future
commits stay single-concern (this review round is four). A squash merge would erase the concern
entirely.
