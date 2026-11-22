<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Documentation Agent Guide

Read this file before you edit, create, or review a page under `docs/`. For repository-wide rules, refer to the root [AGENTS.md](../AGENTS.md).

## What Is Published

The documentation site is built with Fern from the pages listed in `fern/docs.yml`. A page that is not listed there is not published. Other files under `docs/`, such as examples and design notes, are contributor material. Do not move or rename them without checking the links that point to them.

## Never Edit

- `docs/catalog/` is generated from the Go definitions in `karta/sdk/pkg/catalog`. Change the source and run `make generate-samples`.

## Reference Pages Come From Their Source

When the source changes, update the matching page in the same change.

| Page | Source |
|---|---|
| Workload Definition Spec | The CRD schema |
| CLI Command Reference | The output of `kli --help` and each subcommand's `--help` |
| Helm Chart Values | `charts/karta/values.yaml` |
| Karta Operator Flags | The output of the operator `--help` |

## Before You Write a Page

1. Find the most similar existing page and read it in full. Match its structure, heading levels, components, and link style.
2. Open the page with one or two sentences that say what it covers and who it is for. A page that is based on a README or another repository file still needs this intro and the context around it.
3. Put `## Prerequisites` after the intro when the reader needs anything in place first. End a guide or tutorial with `## Next Steps`.
4. Use the exact terms from the code and the library. Check a term against the source before you use it, and do not document something that is deprecated.
5. Link to a page that explains how to start. Do not use a raw API listing as the first link for a developer audience.

## Check Every Claim

- Verify field names, flags, defaults, and behavior against the code, not against another document.
- Run every command and example before you document it. If you cannot run one, say so in your pull request.
- When existing repository content is wrong or out of date, report it to the maintainers. Do not copy the error into the docs, and do not silently change what the source states.
- When you rework a page that is based on existing content, keep every fact, command, and value. Add structure, an intro, and context. Report anything you move or remove.
- Do not write numbers that change, such as the number of definitions or a full list of versions.
- Include an example only when it serves the point of its section.

## Names

- Workload Map is the project name. `Karta` is the CRD kind, and a definition is a `Karta` object.
- Use Karta Operator, `kli` CLI, and Headlamp plugin as component names.
- Use the same name for a component on every page.

## Style

Apply these rules to pages, examples, headings, and release notes that you create or edit. Do not rewrite literal code, identifiers, commands, URLs, or quoted output to satisfy a prose rule. Use the rules to improve clarity, and do not make a change that reduces technical accuracy.

### Voice

- Write in a professional, active voice. Use present tense for product behavior and address the reader as "you".
- Keep sentences short, ideally under 30 words. Use plain English and precise technical terms.
- Define a term the first time it appears. Spell out an uncommon abbreviation on first use.
- Do not use contractions. Write "do not", "cannot", and "is not".
- Do not start a sentence with "Because" or "So".
- Do not use the word "unlike". Write two plain sentences instead of a contrast.
- Do not use filler such as "simply", "just", "in order to", and "it is important to note".
- Do not use marketing words such as "seamless", "powerful", "cutting-edge", "unlock", "leverage", and "comprehensive".
- Do not use "please" in instructions.
- Use "refer to" instead of "see", and "after" instead of "once" for time.
- Use "can" for a capability. Use "may" only for an uncertain outcome, for example "the workload may be preempted".
- Replace Latin terms with plain English: "for example", "that is", "and so on", "through".
- Write "NVIDIA" in capitals, and write "an NVIDIA".
- Write plain, professional prose. Do not copy dictated or casual wording into a page.
- When a section describes what a tool does, state the behavior ("The operator rejects a duplicate definition"). Do not give instructions in that section.

### Procedures and Lists

- Put the condition before the instruction: "To enable suspend and resume, set the field to true", not the reverse.
- Use one action per numbered step.
- Name the thing. Write the flag, field, or path, not "configure the appropriate settings".
- Introduce every list, table, and code block with a complete sentence.
- Use parallel construction in lists.
- Describe user interface steps in text. Do not add screenshots.

### Formatting

- Use title case for headings. Do not style a heading with bold, italics, quotation marks, ampersands, or exclamation marks.
- Format commands, code, filenames, paths, and identifiers as code.
- Use numerals for specific values, parameters, and numbers of 10 or more. Spell out zero through nine in general prose.
- Put a space between a number and its unit.
- Use the Oxford comma.
- Do not use bold for emphasis, emojis, em-dashes, or non-ASCII characters.
- Use `<Note>` and `<Warning>` for callouts. Do not use bold text or blockquotes for a callout, and do not add other components without a reason.
- Start every new Markdown or MDX file with the SPDX and copyright header. In MDX, use `{/* ... */}` comments.

## Navigation and Links

- Register each new page in `fern/docs.yml`. Page paths start with `../docs/` because the configuration file is in `fern/`.
- When you rename a page, rename its title, file, folder, and slug together, update the navigation entry, and fix every link to it.
- When you delete a page, remove its navigation entry and check the repository for links to it, including the README, `AGENTS.md`, the chart, and the skills.
- Use relative links between pages in `docs/`.

## Release Notes

Each release has an entry in `fern/changelog/`, in a file named with the release date, for example `2026-10-21.mdx`.

- Start the file with `tags:` in the frontmatter.
- Use one `##` heading, "Workload Map vX.Y", then `###` sections such as Breaking Changes, Deprecations, New Features, Bug Fixes, and Compatibility.
- Put a breaking change in a `<Warning>` and give the migration steps.
- The entry is a copy of the version's entry in `CHANGELOG.md`, which is the source. Refer to [RELEASE.md](../RELEASE.md).

## Verify

From the `fern/` directory, with the Fern CLI at the version in `fern/fern.config.json`:

| Command | When |
|---|---|
| `fern check` | After any change to `docs/` or `fern/`. CI runs it, and broken links fail it. |
| `fern docs dev` | To preview the site locally. It validates nothing, so run `fern check` as well. |
