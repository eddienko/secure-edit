# Command line

Everything `sedit` does without the full-screen interface: edit one file, print
it, pipe data in and out, share it, and look after files and their passwords. For
the interface, see [interface.md](interface.md).

```
sedit [DIR]                     open the interface (DIR, else $SEDIT_HOME, else this folder)
sedit FILE                      edit FILE (created if it doesn't exist or is empty)
sedit -p FILE                   decrypt FILE to stdout
sedit --info FILE               show FILE's title and description (no password needed)
sedit --set-info FILE --title T change FILE's title and/or description (--description D)
sedit --write FILE              make FILE's contents whatever is piped in on stdin
sedit --append FILE             add whatever is piped in to the end of FILE
sedit --passwd FILE             change FILE's password
sedit --encrypt [-y] FILE       encrypt an existing plaintext FILE in place
sedit --mv OLD NEW              move a file, with its backup and remembered password
sedit --rm [-y] FILE...         delete files, with their backups and remembered passwords
sedit --share FILE --to KEY     write a copy of FILE for someone else (see below)
sedit --import FILE.age -i KEY  turn an age-encrypted file into a sedit file
sedit --keygen -o FILE          create an age key pair for receiving shared files
sedit --remember FILE           edit FILE and save its password in the macOS Keychain
sedit --forget FILE             remove FILE's password from the Keychain
sedit --set-default             store a default password in the macOS Keychain
sedit --forget-default          remove the default password
sedit -h, --help                show usage
sedit --version                 show the version
```

If `FILE` is a symlink, `sedit` follows it: the file it points to is edited,
locked and backed up, and the link itself is left alone.

`--default` or `--custom` can be added to `FILE`, `--passwd` and `--encrypt` to
choose the password for a new file without being asked (see
[Default password](keychain.md#default-password-macos-keychain)).

```sh
sedit secrets.enc            # prompts for a password, opens $EDITOR
sedit -p secrets.enc | grep api
sedit --passwd secrets.enc
```

For a new file you are asked for the password twice. An empty password is
rejected.

## Choosing an editor

`sedit` picks the editor from `$SEDIT_EDITOR`, then `$VISUAL`, then `$EDITOR`,
then `vim` (or `vi` if vim isn't installed). Setting `SEDIT_EDITOR` is the way to
use a different editor for `sedit` than for everything else:

```sh
export SEDIT_EDITOR=vim
```

**Graphical editors are ignored by default.** If `$VISUAL` or `$EDITOR` names VS
Code (`code`, `code-insiders`, `codium`), Cursor, Windsurf, Zed, Sublime Text
(`subl`), Atom, TextMate (`mate`) or BBEdit, `sedit` skips it and tells you:

```
sedit: ignoring VISUAL=code (graphical editors keep plaintext copies); using vim. Set SEDIT_EDITOR to override.
```

The reason is that these editors keep their own history and backup stores
outside `sedit`'s control (VS Code's Local History and hot-exit backups, for
example), which can hold plaintext copies of every version you save. They also
don't fit well with `sedit`: the vim-only extras (title, banner, watermark)
don't apply.

**To use one anyway**, say so explicitly: `SEDIT_EDITOR=code sedit FILE`. Many
graphical editors hand the file to an already running app and **exit
immediately**, which would make `sedit` think you are done: it would find the
file unchanged and delete its temp file while the editor still shows it. For the
editors above `sedit` adds `--wait` for you (so this runs `code --wait`) and
reminds you about the plaintext copies. For any other editor, make the command
wait yourself (look for a `--wait` or `-w` option). If an editor exits within a
second without changing the file, `sedit` warns that it may have detached.

Terminal editors such as nano and emacs are used as they are. Only vim, vi and
nvim get the leak protections described above.

## Environment variables

| Variable             | Effect                                                         |
|----------------------|----------------------------------------------------------------|
| `SEDIT_EDITOR`       | The editor to run, taking priority over `VISUAL` and `EDITOR`. The only way to use a graphical editor. |
| `VISUAL`, `EDITOR`   | The editor to run if `SEDIT_EDITOR` is unset (`VISUAL` first). Graphical editors here are ignored. Default: `vim`, else `vi`. |
| `SEDIT_HOME`         | The folder the interface opens when you give none (`~` at the start is your home folder). A folder on the command line wins. |
| `SEDIT_STATUSLINE`   | Set to `0` (or `false`, `no`, `off`) to turn off the vim banner: the `SEDIT ENCRYPTED` status line banner and the watermark on the first line. Your own status line is left alone. The terminal title and the leak protections stay on. |

To make the opt-out permanent, put `export SEDIT_STATUSLINE=0` in your shell
profile. Anything else, or leaving it unset, keeps the banner.

## Titles and descriptions

A file can have a **title** (a short name for it, one line, up to 200 bytes) and a
**description** (a few lines, up to 2000 bytes). the [interface](interface.md) lists a file by its
title, with the file name after it in dim text, and shows the description in the
preview while the file is locked.

```sh
sedit --title "Work VPN" --description "Office VPN login; rotate every 90 days" vpn.txt
sedit --info vpn.txt                      # prints them, without asking for the password
sedit --set-info vpn.txt --title "Office VPN"      # change one; an empty value clears it
```

`--title` and `--description` give a file you are creating one (a new `FILE`,
`--encrypt`, `--write`, `--append` or `--import`); for a file that exists, use
`--set-info`, which asks for the password. Saving, editing, `--passwd` and
`--mv` leave them as they are. In the interface, the new-file box asks for them
(the description can have several lines) and `i` changes them later.

**They are not secret.** To be shown without the password they sit in the file's
header, readable by anyone who has the file. Don't put anything sensitive in
them. They are authenticated, so a changed title or description makes the file
fail to open, but until it is opened they can't be vouched for: treat what
`--info` or the list shows as a hint. Control characters are refused, and the
interface shows any it finds in a file as dots. `--share` does not pass them on:
the copy for someone else contains only the text.

**Folders** can have a title and description too, in the interface only: see
[interface.md](interface.md). They are kept unencrypted in a hidden `.sedit-folder`
file inside the folder.

A file with a title or description uses format version 2, which sedit before
1.4 cannot open (it says `unsupported file version 2`). Files without them stay
version 1, readable by every version.

## Moving and deleting files

A `sedit` file has things that belong to it: its `FILE.bak` backup, and, if you
used `--remember`, a password stored in the Keychain under the file's path. A
plain `mv` leaves the backup behind and orphans the Keychain entry; a plain `rm`
leaves both. These commands look after all of it:

```sh
sedit --mv secrets.txt archive/old-secrets.txt     # rename or move
sedit --mv secrets.txt archive/                    # into an existing directory
sedit --rm old-notes.txt                           # asks first
sedit --rm -y a.txt b.txt c.txt                    # several files, no question
```

- **`--mv`** moves the file, its `.bak` and its remembered password to the new
  place, and tells you what came along. It works across file systems, and the
  file keeps its permissions.
- **`--rm`** deletes the file, its `.bak`, its remembered password and its lock
  file. It asks for confirmation (`-y` skips that, and is required when there is
  no terminal). **It is not a secure erase:** the file is encrypted, but its data
  may remain in backups and on disk. It deletes only that file's own Keychain
  entry, never the default password.
- **They never overwrite.** `--mv` stops if the destination, or a `.bak` there,
  already exists.
- **Only `sedit` files.** Anything else (a plain file, a directory) is refused, so
  these can't be used as a general `mv` or `rm`. Symlinks are refused too, since
  following one would move or delete the wrong thing; use the real file.
- **`--rm` checks every file first.** If one name is wrong, nothing is deleted.
- A file that another `sedit` has open is refused.
- A problem with the Keychain (it is locked, say) is reported as a warning, not
  an error: the file operation still happens, and an old entry is kept rather than
  lost.

## Piping data in

`--write` and `--append` take the data from stdin, with no editor involved:

```sh
echo "my secret" | sedit --write secret.txt          # replace the contents, like  >
echo "api key: abc123" | sedit --append secret.txt   # add to the end, like  >>
pbpaste | sedit --write notes.txt
sedit -p old.txt | tr a-z A-Z | sedit --write new.txt
```

The file is created if it doesn't exist. Because stdin carries the data, the
password can't come from stdin as well:

- **Existing files:** the password comes from the Keychain (the file's own
  entry, then the default password) or, failing that, `sedit` asks for it on the
  terminal itself (`/dev/tty`), so it works in the middle of a pipeline.
- **New files:** you are asked for a new password, twice, on the terminal. Use
  `--default` or `--custom` to choose without being asked.
- **No terminal at all** (a cron job, for example) and no stored password: it
  stops with a message instead of hanging. Store the password with `--remember`,
  or set a default one with `--set-default`, and it works unattended.

Things to know:

- **The data is stored exactly as it arrives.** Nothing is added, so `echo` adds a
  newline and `printf 'x'` doesn't. `--append` doesn't insert a line break
  either, so a file that doesn't end in a newline gets the new text glued on.
- **Empty input is refused.** If the command before the pipe fails and sends
  nothing, `--write` won't replace your file with nothing.
- Every change keeps the previous version as `FILE.bak`, as an edit does.
- **Piping into a plain `sedit FILE` does not store the data.** There stdin is
  read as the password (see Scripting below), so `echo "my secret" | sedit FILE`
  would make `my secret` the password of a new, empty file. `sedit` warns about
  this when it happens; use `--write` instead.
- `--write` refuses to run when stdin is a terminal, and input is limited to
  256 MiB.

## Converting an existing plaintext file

`sedit` refuses to open a file that isn't a sedit file, and says so before asking
for a password. This protects you from typos like `sedit .bashrc`. To encrypt an
existing plaintext file deliberately:

```sh
sedit --encrypt notes.txt      # asks for confirmation, then a new password
sedit --encrypt -y notes.txt   # skip the confirmation (required without a terminal)
```

It refuses files that are already encrypted or empty. An empty file needs no
conversion: `sedit FILE` treats it as new. No `.bak` is created, because it would
be a plaintext copy. See the limitations below about the old plaintext.

## Sharing a file with someone

A `sedit` file can only be opened with its password. To give someone a copy they
can read without knowing it, encrypt a copy to their public key using
[age](https://age-encryption.org):

```sh
sedit --share secrets.txt --to age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p
sedit --share secrets.txt --to "ssh-ed25519 AAAA... alice@laptop"
sedit --share secrets.txt -R alice.pub -R bob.pub -o for-the-team.age
```

`sedit` decrypts the file in memory and writes the copy, so the plaintext never
touches the disk and no editor is involved. The recipient decrypts it with the
ordinary `age` command and doesn't need `sedit`:

```sh
age -d -i ~/.ssh/id_ed25519 secrets.txt.age      # with their SSH key
age -d -i key.txt secrets.txt.age                # with an age key
```

| Option | Meaning |
|--------|---------|
| `--to KEY` | A recipient's public key: an age key (`age1...`, or a post-quantum `age1pq1...`) or an SSH public key (`ssh-ed25519`, `ssh-rsa`). Repeat it for several recipients. |
| `-R FILE` | A file of public keys, one per line (`#` comments and blank lines are ignored). An SSH `.pub` file works as it is. May be repeated. |
| `-o OUT` | Where to write the copy. Default: `FILE.age` next to the original. `-o -` writes to stdout. An existing `OUT` is replaced. |
| `-a`, `--armor` | Write text (ASCII armor) instead of binary, for pasting into an email or chat. Binary output to a terminal is refused. |

Things to know:

- **It is a snapshot, in one direction.** The recipient gets a copy of the
  contents as they are now. Their changes don't come back, and later changes of
  yours don't reach them. Share again to send an update.
- **Check the key.** Make sure the public key really belongs to the person, for
  example by confirming it with them over another channel. `sedit` can't know.
- **You can't take it back.** Anyone who has the copy and their key can read it,
  for as long as they keep it.
- **No mixing post-quantum and classic keys.** `age` refuses a file addressed to
  both kinds, so give either `age1pq1...` keys only, or classic keys only.
- **Not supported:** `age` plugin recipients (`age1yubikey...`, which need an
  external program) and tagged recipients. Private keys are rejected, and are
  never echoed back.
- `--share` never overwrites the `sedit` file it reads from, even through a
  symlink or hard link, and it leaves the original untouched.
- After sharing, the recipient's copy is only as private as their key and their
  machine. The copy decrypts to plain text on their side.

## Generating a key

To receive files shared with `--share`, or any age-encrypted file, you need a key
pair. `sedit` can make one, so nothing else has to be installed:

```sh
sedit --keygen -o ~/.config/sedit/key.txt
```

```
Public key: age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p
sedit: private key saved to /Users/you/.config/sedit/key.txt (keep it secret)
```

- **Give the public key** (`age1...`) to the people who want to send you files.
  It isn't secret. If you lose track of it, it is also in the `# public key:` line
  of the key file.
- **Keep the key file private.** Anyone who has it can read every file that was
  encrypted to you. It is created with mode `0600`. The key is not passphrase
  protected, so treat the file like an SSH private key and keep it out of
  repositories and cloud-synced folders unless you accept that.
- **`-o FILE` is required**, so a private key never lands in the current
  directory (possibly a git repository) by accident. The directory must already
  exist. An existing file, or a symlink, is never overwritten. `-o -` prints the
  key to stdout instead.
- **`--pq`** makes a post-quantum hybrid key (`age1pq1...`). Anyone sending to it
  must use only post-quantum recipients (see the sharing notes).
- The file has the same format as the one `age-keygen` writes, so the standard
  `age` tools can use it too, for example `age -d -i key.txt file.age`.
- You can also skip this and use an SSH key you already have. Its public key
  (`~/.ssh/id_ed25519.pub`) works as a recipient.

## Importing a shared file

The other direction: if someone sent you a file encrypted with age (for example
made by `sedit --share`), `--import` turns it into a `sedit` file under a password
of your choosing, so you can keep editing it with `sedit`:

```sh
sedit --import secrets.txt.age -i ~/.ssh/id_ed25519    # writes ./secrets.txt
sedit --import message.age -i key.txt -o notes/message.txt
sedit --import team-notes.age                          # passphrase-encrypted file
```

It decrypts in memory and asks you for the new password (or use `--default` or
`--custom`, as for a new file). The plaintext never touches the disk.

| Option | Meaning |
|--------|---------|
| `-i KEY` | Your private key (see [Generating a key](#generating-a-key)): an age key file (`AGE-SECRET-KEY-...` or a post-quantum `AGE-SECRET-KEY-PQ-...`) or an SSH private key. May be repeated. A passphrase-protected SSH key is fine: you are asked for its passphrase, but only if the file is actually addressed to that key. |
| *(no `-i`)* | The file must be passphrase-encrypted (`age -p`); you are asked for the passphrase. |
| `-o OUT` | Where to write the `sedit` file. Default: the input name without `.age`. If the input doesn't end in `.age`, `-o` is required. |

Things to know:

- **It never overwrites anything.** If `OUT` already exists (including a
  symlink), `sedit` stops and tells you. It also refuses to write over the file it
  is reading.
- **Nothing is written on failure.** A wrong key or passphrase, a truncated or
  tampered file, or a file that isn't an age file at all, all stop before the new
  password is even asked for.
- It works on any age file, not just ones from `sedit --share`. Armored (text)
  files are detected automatically.
- The input is read from a file, not from stdin, and is limited to 256 MiB.
- Plugin identities (such as hardware keys) aren't supported.
- The original `.age` file is left as it is. It stays readable by anyone who has
  the key, so delete it if you no longer need it.

## Restoring the previous version

```sh
sedit -p secrets.enc.bak     # inspect it (same password)
mv secrets.enc.bak secrets.enc
```

Only one previous version is kept; each save overwrites `FILE.bak`.

## Scripting

When stdin is not a terminal, passwords are read as lines from stdin, one per
prompt (except with `--write` and `--append`, where stdin carries the data; see
[Piping data in](#piping-data-in)):

```sh
echo "$PASSWORD" | sedit -p secrets.enc
printf '%s\n%s\n' "$OLD" "$NEW" | sedit --passwd secrets.enc   # current, then new
```

No confirmation prompt is shown in that mode. Beware that passwords in shell
variables, history and process lists are visible to other tools.
