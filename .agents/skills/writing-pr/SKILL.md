---
name: writing-pr
description: Writes concise pull request titles and bodies. Use when preparing or editing a pull request title or description.
---

Don't write essays.

- Write a concise pull request body and focus on the final change.
- Use bullet points for the text you do write.
- Do not include that tests were run unless the user asks or repository guidance or the PR template requires it.
- Use Mermaid diagrams, code samples, or snippets when they explain internals or usage better than prose.
- For visual changes, show a before-and-after table with uploaded images or videos.
- For benchmarks, show a before-and-after table using the target branch as the baseline and the PR branch as the candidate.
- Do not mention intermediate PR details, such as reducing a diff from one size to another. Report the final aggregate change.
- For truly impressive, difficult, high-risk, or wide-scoped changes, a technical-blog style body can explain the context and tradeoffs with examples, diagrams, or visuals.
- Use code references where helpful.

## Disclose AI agents

- Disclose any AI agent used to prepare the pull request or contribute to its design, implementation, tests, or documentation.
- Name the agent, tool, or model when known, and briefly describe what it contributed. Do not include private prompts or full chat logs.
- If no AI agent was used, include `AI agents: None`.
- Do not claim that AI-generated work was reviewed or verified unless that review or verification actually happened.
