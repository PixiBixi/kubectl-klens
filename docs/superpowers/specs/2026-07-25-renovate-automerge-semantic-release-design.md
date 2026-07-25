# Automerge des deps + release automatique par commits conventionnels

Date : 2026-07-25

## Objectif

1. Automerger tous les updates Renovate `minor` / `patch` / `digest` (aujourd'hui
   limité au manager `github-actions`).
2. Déclencher automatiquement une nouvelle version quand un update de dépendance
   Go est mergé : `minor` → bump de version **minor**, `patch`/`digest` → bump
   **patch**. Fin du tag `vX.Y.Z` poussé à la main.

## Contexte existant

- `release.yml` : déclenché par un tag `v*` → goreleaser (build cross-platform,
  GitHub release, push krew via `KREW_INDEX_TOKEN`). goreleaser est **l'unique
  publisher**.
- `renovate.json` : `config:recommended`, automerge uniquement pour
  `github-actions`. Renovate émet `fix(deps):` pour les modules Go, `chore(deps):`
  pour les actions.

## Décisions

- **Portée release** : `minor` + `patch` + `digest` des modules **Go** déclenchent
  une release (le binaire compile la dépendance → l'artefact change).
- **GitHub Actions** : automergées mais **jamais de release** — un bump d'action CI
  ne change pas le binaire livré (choix FinOps, évite les releases inutiles).
- **Outil de versioning** : `mathieudutour/github-tag-action` (SHA-pinned) — calcule
  le prochain `vX.Y.Z` depuis les commits conventionnels et pose le tag. goreleaser
  reste seul responsable de la publication (changelog, release, krew). Pas de
  semantic-release Node (surface de maintenance inutile pour un repo Go).

## Design

### `renovate.json`

Mapping type de commit ← type d'update, pour piloter le bump de version :

| Update | Manager | `semanticCommitType` | Effet release |
|---|---|---|---|
| minor | gomod | `feat` | version minor |
| patch / digest | gomod | `fix` | version patch |
| tout | github-actions | `chore` | aucune |

Tout (`minor`/`patch`/`digest`) est automergé via `automergeType: pr` (attend la CI).
`semanticCommits: enabled` explicite pour éviter toute dérive de préfixe. Les groupes
existants (`go-toolchain`, `kubernetes`) sont conservés et n'écrasent pas le type
(hérité de la règle d'update-type).

### `release.yml`

Passage de `on: push tags v*` à `on: push branches [master]` (+ `tags: ['v*']`
conservé comme échappatoire manuelle), un seul job :

1. `checkout` (`fetch-depth: 0`, `persist-credentials: false`).
2. `github-tag-action` (uniquement sur push de branche) : `default_bump: false`
   → aucun commit `feat`/`fix` releasable ⇒ aucun tag, aucune release.
3. `git fetch --tags` puis `setup-go` + goreleaser `release --clean`, gardés par
   `if: github.ref_type == 'tag' || steps.tag.outputs.new_tag != ''`.

**Pourquoi un seul job** : un tag poussé via le `GITHUB_TOKEN` par défaut ne
re-déclenche pas un workflow (garde-fou anti-récursion). Combiner calcul-du-tag +
goreleaser dans le même run évite un PAT dédié au re-trigger. La double condition
préserve le chemin manuel : un tag `v*` poussé à la main par un humain déclenche
directement goreleaser.

## Effet net

- Renovate merge un minor de `client-go` → `feat(deps):` → release **minor** auto.
- Bump patch/digest Go → release **patch** auto.
- Bump d'action CI → mergé, **pas** de release.
- Commits `feat:`/`fix:` manuels → release auto. Plus de tag manuel.

## Hors scope

Pas de migration vers semantic-release Node, pas de changement de `config:recommended`.
