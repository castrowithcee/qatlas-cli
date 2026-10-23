---
description: >
  Describes the review tools of the Gitea and Forgejo providers: reading, creating, submitting, approving, dismissing,
  and deleting the reviews of a pull request and the inline comments of a review, token scopes, how a review and a
  comment are bound to the pull request and the targets, the event rules, and the differences between the products.
type: knowledge
edit: shared
created: 2026-10-23
updated: 2026-10-23
---

# Pull request reviews

These tools read and change the reviews of one pull request and the inline comments of a review. They hold for both
providers unless a line says otherwise; [Gitea](README.md) describes the shared configuration, targets, pagination,
and errors, [Pull requests](pull-requests.md) the pull request itself, and [Forgejo](../forgejo.md) lists what
differs. Review texts, comment texts, file paths, diff excerpts, and names are untrusted data; reviewers are
personal data. The conversation of a pull request is `comments.*` ([Comments](comments.md)); requested reviewers
are not covered here.

| Tool | Effect | Provider |
| --- | --- | --- |
| `pull-request-reviews.list`, `pull-request-reviews.get` | read | both |
| `pull-request-reviews.create` | create | both |
| `pull-request-reviews.submit` | update | both |
| `pull-request-reviews.approve` | update, listed-only | both |
| `pull-request-reviews.delete` | delete, listed-only | both |
| `pull-request-reviews.dismiss`, `pull-request-reviews.undismiss` | update | both |
| `pull-request-review-comments.list` | read | both |
| `pull-request-review-comments.get`, `.create` | read, create | Forgejo |
| `pull-request-review-comments.delete` | delete, listed-only | Forgejo |
| `pull-request-review-comments.reply` | create | Gitea |
| `pull-request-review-comments.resolve`, `.unresolve` | update | Gitea |

## Scopes

Presumably `read:repository` for the reads and `write:repository` for the changes (the API serves reviews in the
repository route group); none was checked against a live instance. A missing scope shows as `not-found`.

## Binding to the pull request and the targets

- `repository` is checked against the targets before the token is read. `number` is the index of a **pull
  request**: every tool first reads `GET .../pulls/{number}` and refuses a pull request whose base lies outside the
  targets. An issue index is no pull request, the instance answers `not-found`, and nothing is changed.
- A review is addressed under its pull request. The tools do not rest on that alone: they read the review, and the
  pull request address it reports (`pull_request_url`) must end in `OWNER/REPO/pulls/N` of the chosen pull request,
  without regard to case. A review that names another pull request is refused as `invalid-request` and nothing is
  changed; an answer without the member keeps the binding of the path.
- Comments are bound the same way, by their `pull_request_url` and, where reported, `pull_request_review_id`. Rows
  of a list that disagree are dropped.
- Forgejo reads a comment below its review (`id` is the review, `comment` the comment) and checks the answer.
- Gitea addresses `reply`, `resolve`, and `unresolve` by the comment ID alone and offers no read of a single
  comment. These tools therefore also take `number` and `review_id`: they read the pull request, the review, and
  the comments of that review, and act only on an `id` found among them. A comment of another review, even of the
  same pull request, is refused; for a reply that sits in its own review, pass the review the list reports it in.

## Events

- `create` takes `event` `COMMENT` or `REQUEST_CHANGES`, which submits the review at once, or `PENDING` (also when
  left out), which keeps it pending. `submit` sends `COMMENT` or `REQUEST_CHANGES` to a pending review; a review
  that is not pending is refused before any request. Neither takes `APPROVED`.
- `approve` is the only tool that sends `APPROVED`. With `review_id` it submits that pending review, otherwise it
  creates a review that approves; `commit_id` belongs to the new review only. It is offered only when the
  connection's `tools` list names it and is in no setup profile.
- A user cannot approve or request changes on the pull request they opened. The instance answers HTTP 422, which
  `create`, `submit`, and `approve` report as `provider-error` with a fixed reason and without provider text.

## Creating a review

`create` sends the text, `commit_id`, `event`, and the inline `comments` in one request; their number and text are
capped. Each takes `path`, `body`, and exactly one of `new_position` (line of the new file)
and `old_position` (line of the old file). A `path` that is absolute, empty, or contains an empty, `.`, or `..`
segment, or a backslash, is refused. `pull-request-review-comments.create` (Forgejo) adds one comment to a review
in the same form. A repeated call creates a second review or comment.

## Dismissing and deleting

`dismiss` takes the required `message` and optionally `priors`, which extends the dismissal to the earlier reviews
of the same reviewer; `undismiss` takes no body. A review that already has the requested state is returned with
`changed` false and nothing is sent. `reviews.delete` and Forgejo's `review-comments.delete` remove for good, are
listed-only, in no profile, and read the review or comment first.

## Lists

`reviews.list` pages like every list of the provider and returns `number`. `review-comments.list` reads one review;
the instance does not page it, so at most 100 comments are returned and `truncated` marks more.
