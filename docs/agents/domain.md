# Domain Docs

This is a single-context repository.

## Before exploring

Read these when they exist and are relevant:

- `GLOSSARY.md` at the repository root;
- ADRs under `docs/adr/`.

If they do not exist, proceed silently. Domain-modeling skills create them lazily when terminology or decisions are actually resolved.

## Conventions

Use terms as defined in `GLOSSARY.md` in code, tests, issues, and design documents. Avoid synonyms the glossary explicitly rejects. If a proposed change conflicts with an ADR, identify that conflict rather than silently overriding the decision.

The expected layout is:

```text
/
├── GLOSSARY.md
└── docs/adr/
    └── NNNN-decision-name.md
```
