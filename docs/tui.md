# sedit-tui

The full-screen terminal interface for [`sedit`](../README.md) files.

`sedit-tui` is a full-screen way to browse and manage a folder of `sedit` files.
It is a separate program, built from the same code as `sedit`, and uses the same
passwords, Keychain entries and editor settings.

```sh
sedit-tui                  # the current folder
sedit-tui ~/secrets        # a folder
sedit-tui --idle 5m ~/secrets
sedit-tui --clipboard-timeout 10s ~/secrets
```

```
┌ ~/secrets ─────────────────┐┌ vpn.txt  (unlocked, locks in 1:41) ────────────┐
│ secrets/                   ││ host=vpn.example.com                           │
│ ├── personal/              ││ user=admin                                     │
│ ├── work/                  ││ token=...                                      │
│ ├── ▪ Work VPN  vpn.txt    ││                                                │
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

A file that has a **title** is listed by it, with the file name after it in dim
text (`▪ Work VPN  vpn.txt`); one without is listed by its name. Titles and
descriptions are readable without the password (they are stored in the clear, so
don't put secrets in them): the locked preview shows the description, and `n`
and `i` set them. See [Titles and descriptions](../README.md#titles-and-descriptions).

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
| `i` | Change the file's title and description in a similar box (asks for the password if it is locked; the text and the password are not touched, and no backup is made). |
| `r` | Rename or move (a new name, or a path relative to the file's folder). Its `.bak` and stored password go with it. |
| `d` | Delete, after a confirmation that defaults to Cancel. Its `.bak` and stored password go too. |
| `p` | Change the file's password. |
| `l` | Lock now. |
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
- Quitting `sedit-tui` clears it at once, and says so.
- The whole text is copied, even past the 1 MiB the preview shows. Binary data and
  empty files are refused.
- **A secret on the clipboard is a risk.** Clipboard managers may record it, and
  on macOS Universal Clipboard can send it to your other Apple devices; clearing
  it afterwards does not undo that, and `sedit` can't mark it as concealed. Copy
  only what you need, and turn off clipboard history for sensitive work if you
  can. (From the command line, `sedit -p FILE | pbcopy` does the same without any
  timer.)

**Colors.** `sedit-tui` uses no colors of its own: everything is drawn in your
terminal's default foreground and background, and emphasis comes from bold and
reverse video (the selected file and the bar at the bottom are reversed) and from
symbols (`▪ ○ ·`). So it follows your theme, whether light, dark or see-through,
and nothing can end up unreadable because a color blends into your background.

**What it does not do (yet).** Sharing, importing, key generation and encrypting a
plain file are only in `sedit`. There is no search inside files, and no mouse.
Files larger than 1 MiB are shown cut off, and binary content is not displayed.

**Security notes.** The interface uses the terminal's alternate screen, so what it
shows isn't left in your scrollback when you quit. But text on screen is visible to
anyone looking, and to screenshots, screen recording and `tmux` logging, so the
usual advice applies: lock (`l`) when you step away. The decrypted text and the
passwords you type are kept in memory that is zeroed on lock, but the terminal
library's own screen buffer holds what is displayed until it is redrawn. As
everywhere in `sedit`, a graphical editor is ignored unless you set
`SEDIT_EDITOR` (see [Choosing an editor](../README.md#choosing-an-editor)).

---

Back to the [README](../README.md): installation, the `sedit` command line,
sharing, Keychain support, file format and limitations.
