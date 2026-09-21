# Spaced-repetition review

A memory can be enrolled in a review deck and quizzed on a schedule, turning
rekam from passive storage into an active recall aid. `internal/db/srs.go`
implements the SM-2 scheduling algorithm and deck queries; the HTTP surface
is `GET /review` (due queue + stats), `POST /review/{id}` (enroll),
`DELETE /review/{id}` (drop), and `POST /review/{id}/grade` (grade and
reschedule). See `spec/memory.md` for the underlying memory record.

## The model

Enrolling a memory (`AddToReview`) creates one `srs` row keyed by memory id,
seeded at ease 2.5, interval 0, reps 0, due immediately. Re-enrolling an
already-enrolled memory is a no-op that preserves its existing schedule
rather than resetting it — a stray double-click, or the client re-issuing
the enroll call, costs nothing.

`DueReviews` returns every enrolled memory whose `due_at` has passed,
soonest-due first, with full content attached (so the caller can show the
answer), plus separate `total`/`due` deck stats — the deck's true size does
not shrink just because a `limit` caps how many cards come back in one
batch.

`Grade` applies one SM-2 update per call, given a grade in `[0,5]`:

- **Pass (grade ≥ 3)**: `reps` increments. Interval schedule is 1 day on the
  first pass, 6 days on the second, and `round(previous_interval * ease)`
  (floored at 1 day) on every pass after that — each confident recall pushes
  the card further out.
- **Lapse (grade < 3)**: `reps` and `interval_days` both reset to 0 and the
  card is due again immediately — a failed recall brings the card straight
  back into rotation rather than merely shortening its interval.
- **Ease**, in both cases, adjusts by the standard SM-2 formula
  `ease += 0.1 - (5-grade)*(0.08 + (5-grade)*0.02)`, floored at `1.3`
  (`minEase`) so a card that keeps getting graded at the minimum passing
  grade never collapses to an ever-shrinking interval.

Grading a memory not currently enrolled, or with a grade outside `[0,5]`,
is rejected rather than treated as an implicit re-enroll or clamped value.
`RemoveFromReview` drops the `srs` row only — the underlying memory record
is untouched.

Enrolling requires the caller to be able to read the memory (see
`spec/teams.md`'s `Scope`): a memory outside the caller's scope is reported
as not found, exactly like an id that doesn't exist at all, so a restricted
caller can't use enrollment to probe which ids exist elsewhere. The deck
itself lives in the caller's own memory store, so grading/removal only ever
act on that identity's own enrolled cards.

## RVW-01: Enrolling a memory adds it to the deck, due immediately

When a memory is enrolled via `POST /review/{id}`,
then the response is `200` with the new scheduling state (ease 2.5,
interval 0, reps 0), and the memory now appears in `GET /review`'s due
queue right away.

## RVW-02: Re-enrolling an already-enrolled memory preserves its schedule

Given a memory that has been enrolled and graded at least once (so its
`reps`/`interval_days` have advanced past the initial defaults),
when `POST /review/{id}` is called again for the same memory,
then the response is `200` with the schedule unchanged from before the
second call, and the deck still contains exactly one card for it — not two.

## RVW-03: A confident recall lengthens the interval each time

Given a memory freshly enrolled,
when it is graded 5 three times in a row (via
`POST /review/{id}/grade`),
then each grade's `interval_days` is strictly greater than the previous
one's, and after the third grade the card no longer appears in the due
queue — the whole point of spacing is that material recalled well keeps
coming back less often.

## RVW-04: A failed recall resets the card and brings it back immediately

Given a memory that has built up a comfortable interval through several
passing grades,
when it is then graded below 3 (a lapse),
then the response's `interval_days` drops (strictly less than the
interval before the lapse) and `reps` resets to 0, `due_at` is at or before
now, ease does not increase, and the card reappears in the due queue right
away.

## RVW-05: Ease never drops below the SM-2 floor

Given a memory graded repeatedly at the minimum passing grade (3),
then across many such grades the reported `ease` converges to, but never
drops below, `1.3`.

## RVW-06: Dropping a card removes it from the deck without touching the memory

When an enrolled memory is dropped via `DELETE /review/{id}`,
then the response is `200`, the deck's `total` count decreases and the
memory no longer appears in `GET /review`, but the memory record itself is
still readable and unchanged via `GET /memory/{id}` — and grading the
dropped card afterward fails rather than silently re-enrolling it.

## RVW-07: The due queue and deck totals are reported separately

Given three memories enrolled and one of them graded to a future due date,
when `GET /review` is called,
then `stats.total` counts all three enrolled cards while `stats.due` counts
only the two still due now, and the returned `cards` array contains only
the due ones (not the whole deck) — a `limit` query parameter caps how many
cards `cards` returns without changing `stats.total`.

## RVW-08: Grading rejects out-of-range or non-numeric input without disturbing the deck

Given an enrolled memory,
when `POST /review/{id}/grade` is called with a grade outside `[0,5]` (e.g.
-1, 6, 99),
then the response is an error (`4xx`) and the card's schedule is left
exactly as it was; a non-numeric `grade` field is rejected as `400` rather
than being coerced to zero.

## RVW-09: Enrolling or grading an unknown memory is a not-found, not a phantom card

When `POST /review/{id}` or `POST /review/{id}/grade` is called with an id
that does not correspond to any memory,
then the response is `404`, and no `srs` row is created.

## RVW-10: Enrollment respects the caller's read scope

Given a restricted team member whose scope excludes a taxonomy branch a
memory lives in,
when they attempt `POST /review/{id}` for that memory,
then the response is `404` — indistinguishable from the id not existing at
all — and the memory is not added to anyone's deck as a side effect; the
enrolling identity's own deck (e.g. the owner's) is unaffected by the
attempt.

## RVW-11: Review routes require authentication

When any of `GET /review`, `POST /review/{id}`, `DELETE /review/{id}`, or
`POST /review/{id}/grade` is called without credentials,
then the response is `401` for every one of them.
