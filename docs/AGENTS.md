<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Documentation Agent Guide

Read this file before you edit, create, or review a page under `docs/`. For repository-wide rules, refer to the root [AGENTS.md](../AGENTS.md).

## What Is Published

The documentation site is built with Fern from the pages listed in `fern/docs.yml`. A page that is not listed there is not published. Other files under `docs/`, such as examples and design notes, are contributor material. Do not move or rename them without checking the links that point to them.

## What Each Section Is For

| Section | What belongs here |
|---|---|
| Overview | What Workload Map is, its concepts, its features, and its architecture. No steps. |
| Getting Started | What to have in place, how to install each component, and the first run: the quickstarts and the first tutorial. |
| Observability | How to see workloads with the `kli` CLI and the Headlamp plugin. |
| Building on Workload Map | How to write a definition for a workload type, and how to build a controller with the Go library. |
| Configuration | How to set up and tune a component after it is installed. Today this is the Karta Operator only. |
| Reference | Facts to look up: commands, flags, fields, values, and the catalog. |
| Troubleshooting | An error, its cause, and its fix. |

To place a new page, ask what the reader does with it: understand, install, look at workloads, build something, tune a component, look something up, or fix an error. Pick the section for that answer. Use the table in Where Docs Go to find the page to update for a change.

## Where Docs Go

`docs/design/`, `docs/keps/`, and `docs/examples/` are contributor material. They are not published. Do not document user-facing behavior only there. A flag, a column, or a default that a user can see belongs on a published page.

The following table lists where a change goes. Update every page in the row, in the same pull request as the code change.

| If your change | Update |
|---|---|
| Changes what a user needs before installing, such as a Go version, a Kubernetes version, or a required tool | Getting Started > Prerequisites, in the section for the component. Search `docs/` for the old value and update every page that states it |
| Changes how to install a component, or adds an install method, a chart install step, or a release artifact | Getting Started > Installation, in the section for the component |
| Adds a user-facing feature | Overview > Key Features. Add it to Core Concepts too when it adds a new concept |
| Changes the output, a flag, or the behavior of a command that a quickstart runs, or a Go call or the example code that a quickstart quotes | Getting Started > Quickstarts, so the output and the code that the page shows stay exact |
| Adds or changes a `kli` command, flag, column, or output | Reference > CLI Command Reference, and Observability > Inspect Workloads with the CLI when a task changes |
| Changes the Headlamp plugin | Observability > Inspect Workloads in Headlamp |
| Adds or changes a field of the `Karta` resource | Reference > Workload Definition Spec, and Building on Workload Map > Define a Workload Type when the steps change |
| Adds or changes a catalog definition | Reference > Catalog of Definitions. Do not edit `docs/catalog/` |
| Adds or changes a Helm chart value | Reference > Helm Chart Values |
| Adds or changes a Karta Operator flag | Reference > Karta Operator Flags |
| Adds an operator setup option, such as certificates or FIPS | Configuration > Karta Operator, as a new page or a section of one, and the matching entry in Helm Chart Values or Karta Operator Flags |
| Adds or changes an exported Go library function or type | Its Go doc comment, which feeds the complete API reference on pkg.go.dev. Update Building on Workload Map > Use the Go Library only when a call that a controller uses changes |
| Adds an error that a user can hit | Troubleshooting > Troubleshoot Definitions for validation errors, Troubleshoot jq Paths for path errors, or Troubleshoot the Go Library for library errors |
| Is a breaking change or a deprecation | The release notes, with the migration steps |

If no row fits, find the closest section in `fern/docs.yml` and ask the maintainers before you add a new section. When the navigation changes, update this table.

## Add a Page or a Section

The sidebar of the site comes from the `navigation:` list in `fern/docs.yml`. You can add a page, or a group of pages, when the current structure does not fit your change.

To add a page to an existing section or group:

1. Create the file under `docs/`, in the folder of that section.
2. Add a `- page:` entry in `fern/docs.yml`, inside the `contents:` list of that section or group:

   ```yaml
   - page: Run in FIPS Mode
     path: ../docs/configuration/karta-operator/run-in-fips-mode.mdx
     slug: run-in-fips-mode
   ```

To add a new group of pages, add a `- section:` entry with its own `contents:` list, and put your page inside it. For example, a group for Headlamp plugin settings goes inside Configuration, next to the Karta Operator group:

```yaml
- section: Headlamp Plugin
  slug: headlamp-plugin
  contents:
    - page: Configure the Headlamp Plugin
      path: ../docs/configuration/headlamp-plugin/configure-the-headlamp-plugin.mdx
      slug: configure-the-headlamp-plugin
```

Add a row to "What Each Section Is For" only when you add a new top-level section, such as Overview or Reference. When you add a page, check whether "Where Docs Go" needs a row for the kind of change that it covers. Run `fern check` after any change to `fern/docs.yml`.

## How File Names, Titles, Navigation, and Slugs Fit Together

A page has five names. Each one has a different job, and only the slug sets the URL.

| Name | Where it is set | What it does |
|---|---|---|
| Folder and file name | The path under `docs/` | Locates the source file. It does not appear in the URL. For example, the folder `docs/about/` appears as `/overview/` on the site. |
| Title | `title:` in the page front matter | The title of the page. It can be longer than the sidebar text. For example, the page title "Core Concepts of Workload Map" appears as "Core Concepts" in the sidebar. |
| Sidebar title | `sidebar-title:` in the page front matter | The text of the page in the sidebar. When it is set, the sidebar shows it and ignores the navigation label. |
| Navigation label | `page:` in `fern/docs.yml` | The name of the page in the navigation list. The sidebar shows it only when the page has no `sidebar-title`. Keep it the same as the sidebar title. |
| Slug | `slug:` in `fern/docs.yml` | The last part of the URL. The full URL is the slug of each section, then the slug of the page, for example `/observability/inspect-workloads-with-the-cli`. |

Follow these conventions so that the names stay consistent:

- Name the file after the page, in lowercase with hyphens, and use the same text as the slug. For example, the page "Run in FIPS Mode" is in `run-in-fips-mode.mdx` and has the slug `run-in-fips-mode`.
- Put the file in the folder that matches its section, for example `docs/configuration/` for Configuration. The exception is Overview, which uses `docs/about/`.
- Set `slug:` on every page and every section in `fern/docs.yml`. Without it, Fern builds the slug from the label, not from the file name. For example, without a slug the page labeled "CLI" gets a URL that ends in `/cli`, not in `/quickstart-cli`, and a later rename of the label changes the URL.
- When you rename a page, rename the title, sidebar title, navigation label, file, and slug together, as described in Navigation and Links. A changed slug changes the URL, and every link to the old URL breaks.

## Choose the Page Type

- **Reference** pages list facts to look up: commands, flags, fields, and values. A new flag or column is a reference entry. It is not a guide.
- **Guides** help a reader finish a task that they could not finish before, in steps. Write a guide when your change gives users a new task to do. Examples are Define a Workload Type and Configure Webhook Certificates.
- **Tutorials** teach by building one working result from scratch. Write one when all of the following are true:
  1. The reader ends with one complete, working result.
  2. The reader can follow it on a local setup, such as a kind cluster, with nothing existing in place.
  3. The task needs more than one concept at once, so a single guide cannot teach it. Examples are Write Your First Workload Definition and Build a Controller on Kind.
- **Troubleshooting** pages list an error, its cause, and its fix.
- Add the change to the page that already covers the task before you create a page. A new page needs a place in `fern/docs.yml`.

## Write the Intro

The text between the title and the first heading answers three questions, in this order:

1. What the page covers, or what the thing is. State it in one or two sentences, and use the same terms as the code.
2. When the reader needs it, or what they will do. For a guide, write "In this guide, you ..." and name the result.
3. Where to go for related information. Link to the reference page, to the tutorial for a newcomer, or to the guide for the next task.

Add `## Prerequisites` after the intro when the reader needs something in place first, and end a guide or tutorial with `## Next Steps`. The page front matter needs a `description` that starts with "Learn how to" for a guide, "Look up" for a reference page, and "Find the cause and the fix" for troubleshooting. For an example, read the intro of Define a Workload Type.

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
2. Write the intro as described in Write the Intro. A page that is based on a README or another repository file still needs this intro and the context around it.
3. Use the exact terms from the code and the library. Check a term against the source before you use it, and do not document something that is deprecated.
4. Link to a page that explains how to start. Do not use a raw API listing as the first link for a developer audience.

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
