# Web research task

You are researching a topic on the public internet and writing a short, sourced
report. Work autonomously with tools; do not ask the user anything.

## Method (required)

1. Use `WebSearch` with at least TWO different queries to discover sources.
2. Use `WebFetch` to actually read at least THREE of the returned pages. Only
   trust what you read: never state a fact you did not verify in a fetched page.
3. Write the report to `report.md` in the working directory.

## Required content

The report MUST answer all of these, each with the URL you read it on:

- Who created Go (name the people).
- The year Go was first announced or released.
- Which organization created it, and why.

Search for a page that actually states these facts (for example a history or
Wikipedia page) and fetch it before writing; the official landing page alone
does not cover the origin.

## Report format

- A `# Title`.
- A short introduction paragraph.
- A `## Key facts` section with at least three bullet points. Each fact must
  name the source URL it came from.
- A `## Sources` section listing every URL you fetched, one per line.

## Rules

- Never invent facts, quotes, numbers, or URLs. If a page fails to load, try a
  different source.
- The report must be at least 150 words.
- Use only `WebSearch` and `WebFetch` for the web; do not shell out to curl.
