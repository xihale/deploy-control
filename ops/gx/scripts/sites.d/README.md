# sites.d — deploy recipes (the "PKGBUILD"s)

Every site registered in deployd points `script:` at `../run-site` (the
"makepkg"). At deploy time the runner reads this directory's recipe named
after `DEPLOY_SITE`, then runs up to three phases — `prepare`, `build`,
`publish` — with `cwd` = deployd's checked-out worktree at exactly
`DEPLOY_COMMIT`.

A recipe is plain bash that sets variables and, where the defaults don't
fit, defines phase functions. Shared helpers ("prefabricated functions")
come from `../lib/common.sh`.

## Variables

| variable           | meaning                                                       |
| ------------------ | ------------------------------------------------------------- |
| `site`             | REQUIRED. Must equal the dispatched `DEPLOY_SITE` (sanity gate) |
| `dest`             | Publish target dir; enables the default `publish_tree`        |
| `out`              | Build output dir for the default publish, default `dist` (`out=.` for source trees) |
| `attic_trees=(…)`  | Immutable-hashed trees handled with the attic policy          |
| `attic_dir`        | Attic dir, default `$HOME/$SITE-attic`                        |
| `publish_excludes=(…)` | Extra rsync `--exclude`s for the main pass                |

## Phases

- `prepare` — install deps, fill caches, bootstrap toolchains. Optional.
- `build` — compile. Optional (pure-static sites skip it).
- `publish` — ship artifacts. Optional **if** `dest` is set: the default
  `publish_tree "$out" "$dest"` runs instead (attic policy + age pruning +
  `deploy-meta.json`). Sites that publish services (foyer, deploy-control)
  override `publish` and manage their own process lifecycle.

A failure in any phase (or in deployd before them) notifies `🚨 [site]`
with the reason; success notifies `[site] [commit7]` via ntfy (see
`dc_notify`).

## Helpers (lib/common.sh)

`say` `die` `dc_notify` `publish_tree` `write_meta` `go_env` `fetch_file`
`lfs_pull` `check_lfs_pointers` `user_bus_env` — read `lib/common.sh` for
exact contracts. Adding a helper there beats copy-pasting logic into
recipes; keep recipes declarative unless a site genuinely needs custom code
(zig-playground's compiler fill and foyer's service dance are the examples).
