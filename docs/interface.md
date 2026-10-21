# The interface

`sedit` with no file, or with a folder, is a full-screen way to browse and manage
a folder of encrypted files. It is the same program as the [command line](command-line.md)
and uses the same passwords, Keychain entries and editor settings.

```sh
sedit                      # $SEDIT_HOME if it is set, otherwise the current folder
sedit ~/secrets            # a folder
sedit --idle 5m ~/secrets
sedit --clipboard-timeout 10s ~/secrets
```

To always open the same place, put `export SEDIT_HOME=~/secrets` in your shell
profile. A folder given on the command line wins over it. The interface needs a
terminal: `sedit` with no arguments in a script or a pipe says so.

```
┌ ~/secrets ─────────────────┐┌ vpn.txt  (unlocked, locks in 1:41) ────────────┐
│ secrets/                   ││ host=vpn.example.com                           │
│ ├── personal/              ││ user=admin                                     │
│ ├── work/                  ││ token=...                                      │
│ ├── ▪ Work VPN             ││                                                │
│ ├── · readme.md            ││                                                │
│ └── ○ team.age             ││                                                │
└────────────────────────────┘└────────────────────────────────────────────────┘
 ↑↓ move  ⏎ unlock  e edit  n new  r rename  d delete  p password  l lock  ? help  q quit
```

The list shows folders, then files. `▪` is a `sedit` file, `○` an age file (turn
it into a `sedit` file with `sedit --import`) and `·` anything else. Backups
(`.bak`), lock files and dot files are hidden.

Files are in alphabetical order of what is shown: the title if there is one, the
name otherwise.

A file that has a **title** is listed by it (`▪ Work VPN`); one without is listed
by its name. The name of the selected file is in the preview's title
(`Work VPN · vpn.txt`), and pressing `v` shows every titled file's name under its
title, in dim text. Titles and
descriptions are readable without the password (they are stored in the clear, so
don't put secrets in them): the locked preview shows the description, and `n`
and `i` set them. See [Titles and descriptions](command-line.md#titles-and-descriptions).

**Nothing is decrypted until you ask.** Selecting a file shows `LOCKED` (with its description, if it has one). Press
**Enter** to unlock it: if its password is stored in the Keychain, or the default
password fits, it opens at once; otherwise you are asked (what you type is shown
as dots). The preview then shows the text, and you can scroll it with **Tab**.

| Key | Action |
|-----|--------|
| `↑` `↓` `Home` `End` | Move (`g` and `G` also go to the top and bottom). |
| `→` `←` | Open and close a folder; `←` on a file goes to its folder. |
| `Enter` | Unlock and show the file (or open or close a folder). |
| `e` | Edit in your editor: unlocks first, then runs the same editing as `sedit FILE`, including the vim protections and the `SEDIT ENCRYPTED` banner. The previous version is kept as `.bak`. |
| `y` | Copy the whole text to the clipboard (unlocks first if needed). It is cleared again after 30 seconds; see **Clipboard** below. |
| `/` | Filter the list. Type words and only the files whose title or name contain all of them (any case) stay, with the folders leading to them opened for you; searches the whole tree, up to 5000 entries. `↑` `↓` pick a match while you type, **Enter** keeps the filter and goes back to the list, **Esc** removes it (also from the list). The first match is selected, and the one you were on stays selected if it still matches. A file that was just made or moved and doesn't match clears the filter rather than disappear. |
| `n` | New file in the selected folder. Asks for a name, a title and a description in one box (Tab and Shift-Tab move between the fields; the title and description are optional; the description can have several lines, with Enter starting a new one, and **Ctrl-S** or Enter in the name or title creates the file), then a password (the default, or one typed twice), then opens the editor. Esc cancels. A new file left empty is not created. |
| `N` | New folder in the selected folder. Asks for a name, a title and a description in one box, like `n`. The folder is private to you (mode 0700). |
| `m` | Move the selected file or folder to another folder, chosen from a list (type to narrow it; the folder it is in, and for a folder itself and everything inside it, are left out). A folder moves with everything in it, and the passwords remembered for its files are re-filed. A folder can't be moved to another file system. |
| `i` | Change the title and description of a file (in a similar box; asks for the password if it is locked; the text and the password are not touched, and no backup is made) or of a folder (no password: see below). |
| `r` | Rename a folder (a new name; use `m` to move it), or rename or move a file (a new name, or a path relative to the file's folder). Its `.bak` and stored password go with it. |
| `d` | Delete a file, after a confirmation that defaults to Cancel; its `.bak` and stored password go too. A folder can be deleted only when it is empty (a leftover `.DS_Store` doesn't count); otherwise the bar says what is in it. |
| `p` | Change the file's password. |
| `l` | Lock now. |
| `v` | Show the names of titled files under their titles, in dim text, or go back to titles only. The list starts with titles only each time. A file without a title stays on one row, and a folder is listed by its title in both. |
| `R` | Re-read the folder from disk. |
| `J` `K` | Scroll the text a line down or up, without leaving the list. |
| `PgDn` `PgUp` `Space` | Scroll the text a page, from the list, while a file's text is shown (on a locked file or a folder they page the list as usual). `Space` goes down. |
| `Tab` | Move into the text to scroll it with the arrow keys (`Esc` or `Tab` returns to the list). `y` works there too. |
| `?`, `q` | Help, quit. |

**The bar at the bottom** shows the keys that matter for what is selected: a
folder, a locked file, a file that is open, or the text when you are scrolling it.
It shows as many as fit, in order of usefulness, and `? help  q quit` is always
there. Messages replace it for a moment.

**The preview's title.** For a file with a title, the pane's title is the title
and the file name (`Work VPN login · vpn.txt`) followed by `(locked)`, or by how
long until it locks once the file is open. The description, if
there is one, stays pinned above the text (up to three lines, with a rule under
it). When the text is longer than the pane, `lines 3-16 of 60` is shown on the
bottom border.

**Folders.** A folder can have a title and description like a file, shown the same
way: `Banking and cards` in the list (the folder's name is in the preview's title),
the title in the preview's title,
the description and what is inside (`2 sedit files, 1 folder`) in the preview. They
are kept in a small hidden file, `.sedit-folder`, inside the folder, so they move
with it. Unlike a file's, they are not encrypted or authenticated: anyone who can
write in the folder can change them, and they are for finding your way around, not
for secrets. The folder you opened can have them, but can't be
renamed, moved or deleted from the interface. Folders are never deleted while
they hold anything but this file, `.DS_Store` and empty lock files. The `/`
filter matches a folder's title and name too, and shows everything in a folder that
matches.

**Locking.** Only one file is unlocked at a time. A file locks again when you move
to another one, with `l`, when you quit, and after **2 minutes without a key
press** (change that with `--idle`, for example `--idle 30s`). Locking zeroes the
decrypted text and the password in memory. While you are in the editor the timer
doesn't run.

**Clipboard.** Selecting text with the mouse in the preview also selects the file
list beside it, because a terminal selects whole rows. So press `y` to copy the
whole decrypted text instead (or `e` to open it in your editor).

- The text goes to the system clipboard through `pbcopy` on macOS (or
  `wl-copy`, `xclip` or `xsel` on Linux), on standard input and never on a command
  line. Without one of those tools `y` says so and does nothing.
- It is **cleared after 30 seconds**, or whatever `--clipboard-timeout` says
  (at least 1s), and **only if the clipboard still holds that text**: something
  you copied in the meantime is never wiped. Copying again restarts the countdown.
- Quitting the interface clears it at once, and says so.
- The whole text is copied, even past the 1 MiB the preview shows. Binary data and
  empty files are refused.
- **A secret on the clipboard is a risk.** Clipboard managers may record it, and
  on macOS Universal Clipboard can send it to your other Apple devices; clearing
  it afterwards does not undo that, and `sedit` can't mark it as concealed. Copy
  only what you need, and turn off clipboard history for sensitive work if you
  can. (From the command line, `sedit -p FILE | pbcopy` does the same without any
  timer.)

**Colors.** The interface uses no colors of its own: everything is drawn in your
terminal's default foreground and background, and emphasis comes from bold and
reverse video (the selected file and the bar at the bottom are reversed) and from
symbols (`▪ ○ ·`). So it follows your theme, whether light, dark or see-through,
and nothing can end up unreadable because a color blends into your background.

**What it does not do (yet).** Sharing, importing, key generation and encrypting a
plain file are only in the [command line](command-line.md). There is no search
inside files, and no mouse.
Files larger than 1 MiB are shown cut off, and binary content is not displayed.

**Security notes.** The interface uses the terminal's alternate screen, so what it
shows isn't left in your scrollback when you quit. But text on screen is visible to
anyone looking, and to screenshots, screen recording and `tmux` logging, so the
usual advice applies: lock (`l`) when you step away. The decrypted text and the
passwords you type are kept in memory that is zeroed on lock, but the terminal
library's own screen buffer holds what is displayed until it is redrawn. As
everywhere in `sedit`, a graphical editor is ignored unless you set
`SEDIT_EDITOR` (see [Choosing an editor](command-line.md#choosing-an-editor)).

---

See also the [README](../README.md), the [command line](command-line.md),
[remembering passwords](keychain.md) and [security and file format](security.md).
