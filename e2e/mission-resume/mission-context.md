# Mission guide: batch processing through a local job API (resumable)

You are running a long, unattended batch mission. The process may be killed and
restarted at any point; your durable progress lives in `results.jsonl`. Follow
this guide exactly.

## Environment

- All input files are in the current working directory.
- The job API is a local HTTP service at `http://127.0.0.1:8799`.
- Use the `HTTPRequest` tool for every API call. Do NOT use `Bash` or `curl` for
  the API: `HTTPRequest` is the reliable tool for this.

## Input

`dataset.jsonl` has one JSON object per line:

    {"id":"item-01","prompt":"red fox","expect":"red fox-ok"}

Process EVERY line. Never drop or reorder items.

## Job API contract

1. Submit a job:

       POST http://127.0.0.1:8799/jobs
       headers: {"Content-Type":"application/json"}
       body:    {"prompt":"<the item prompt>"}
       -> 201 {"id":"job-N","status":"running"}

2. Poll the job until it is no longer `running`:

       GET http://127.0.0.1:8799/jobs/<id>
       -> 200 {"id":"job-N","status":"running" | "done" | "failed"}

   A job stays `running` for a couple of polls: keep polling with the same id
   until the status is `done` or `failed`. Do not submit a new job just because
   the first poll is still running.

3. Fetch the result once the job is `done`:

       GET http://127.0.0.1:8799/jobs/<id>/result
       -> 200 {"id":"job-N","prompt":"...","value":"<value>"}
       -> 409 if the job failed, 425 if it is still running.

The service randomly fails the first attempt of some jobs (status `failed`).
When a job fails, submit the SAME prompt again as a new job and poll it. Retry
up to 3 attempts per item; only give up after 3 failures and record the item
with status `failed`.

## Verification rule

An item is correct only when the fetched `value` equals the item's `expect`
field. Never mark an item done before you have fetched its result and compared
`value` with `expect`.

## Output (durable, resumable progress)

Maintain `results.jsonl` in the working directory: one JSON object per line,
appended after each item is verified:

    {"id":"item-01","job_id":"job-1","value":"red fox-ok","attempts":1,"status":"verified"}

Rules:
- On start, if `results.jsonl` exists, read it and SKIP every item whose `id`
  already has a `verified` line. This is what makes the mission resumable after
  a crash; the file is the source of truth, not your memory.
- Append one line per item, in dataset order. Do not rewrite or reorder lines.
- Use `Read` to check the file and `Write`/`Edit` to append.

## Checklist and mission lifecycle

1. Call `TodoWrite` once with a short plan. Keep every TODO entry to at most
   6 words and never paste this guide into a TODO. Example entries:
   `process item-01`, `process item-02`, `process item-03`, `finish`.
2. Call `MissionStart` with the goal: "Process every item in dataset.jsonl
   through the local job API and record only verified results in results.jsonl."
3. Work one item at a time, using `HTTPRequest` for the API calls. After each
   verified item, update its TODO to `completed`.
4. Do not stop with plain text while items remain. Keep calling tools. If a
   reply would be empty, call the next tool instead.
5. After every item has a verified line in `results.jsonl`, call
   `MissionComplete` with a short summary (items processed and retries used).

## Success criteria

- `results.jsonl` contains exactly one `verified` line per dataset item.
- Every `value` equals its `expect`.
- No item is missing, duplicated, or unverified.
