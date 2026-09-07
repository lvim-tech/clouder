# clouder

Bidirectional folder sync between a **local folder** and a **cloud folder**,
with a first-class two-column diff. A sibling of
[configer](https://github.com/lvim-tech/configer): same Go/Bubble Tea shape,
same CLI feel — but where configer has a source-of-truth tree, clouder has two
**equal peers** and a **state file** that remembers the last sync, so it can
tell a local edit from a remote delete.

Providers are modular. Dropbox is implemented; MEGA and others plug in behind
the same interface without touching the engine.

## Model

- A **pair** is one `local folder ⇄ provider:account:remote folder`.
- Change detection is by **provider content hash** (Dropbox's 4 MiB-block
  hash), never by mtime — so local and remote compare without downloading.
- The **state file** (`~/.local/state/clouder/<pair>.json`) is the snapshot of
  the last successful sync. Reconciliation is a three-way comparison
  (state · local · remote) that classifies every path and picks a direction.
- **Conflicts are never resolved silently.** A file changed on both sides — or
  changed on one and deleted on the other — is skipped and shown, until you say
  `--up --force` or `--down --force` whose version wins.

## Commands

```
clouder                      the interactive settings TUI
clouder --diff [pair]        the two-column diff: local | remote
clouder --inventory [pair]   what state every file is in, compactly
clouder --sync [pair]        make both sides agree
clouder --add-account <provider> [account]   log in, store the token in pass
clouder --init               write a starting config.toml
```

Flags: `--dry-run`/`-n` reports without touching anything; `--up`/`--down`
restrict a sync to one direction; `--force` (with a direction) resolves
conflicts that way.

## The diff

```
notes  (~/Documents/notes  ⇄  dropbox:default:/notes)
   ST  PATH                     LOCAL                       REMOTE
   ↑~  todo.md                  2026-09-07 09:02   1.8 KiB  2026-09-01 12:40   1.6 KiB
   ↑+  drafts/clouder.md        2026-09-07 10:55    12 KiB  —
   ↓+  mobile/photo-list.txt    —                           2026-09-05 18:03     927 B
   ↑✕  old/scratch.txt          —                           2026-08-30 11:20   2.0 KiB
   !   plan.md                  2026-09-07 08:11   6.1 KiB  2026-09-06 23:47   5.9 KiB  CONFLICT

↑ up 2   ↓ down 1   = in sync 1   ! conflicts 1
```

Marks: `↑` local→remote, `↓` remote→local; `+` new, `~` modified, `✕` delete;
`=` in sync, `!` conflict. Each side shows its last-modified date and a
human-readable size; the side a delete will destroy still shows what is about
to be lost.

## Setup (Dropbox)

1. Create a Dropbox app at <https://www.dropbox.com/developers/apps> — *Scoped
   access*, *Full Dropbox* (or *App folder* to confine clouder). Enable
   `files.metadata.read/write` and `files.content.read/write`.
2. Run **`clouder`** — the settings TUI. Press `p` to paste the app key, `A` to
   add an account (it shows the OAuth URL and takes the code right there), `a`
   to add a pair, then `w` to save. Everything lands in
   `~/.config/clouder/config.toml`; the token goes to `pass` under
   `clouder/dropbox/<account>`. **No app secret** is ever used (OAuth PKCE).
3. `clouder --diff` to preview, `clouder --sync` to apply.

The same steps work from the command line if you prefer: `clouder --init`, edit
the file by hand, `clouder --add-account dropbox <account>`.

## Build

```sh
go build -o ~/.local/bin/clouder ./cmd/clouder
```

## Status

Phase 1 (engine, Dropbox provider, CLI, two-column diff) and the Phase-2
settings TUI (manage pairs, set the provider key, log an account in) are
implemented and unit-tested. Planned: the diff/sync panes inside the TUI, and
MEGA plus `upload_session` for files over 150 MB (Phase 3). See
`.claude/plan/2026-09-07/clouder-plan.md`.

## License

MIT
