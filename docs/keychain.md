# Remembering passwords (macOS)

Typing a password every time gets tedious. On macOS `sedit` can keep passwords in
your login Keychain, either one per file or a default for new files. Both are
opt-in, and both are readable by any program running as you, so read the trade-off
first. The interface uses them too: a file whose password is stored opens with
Enter and no prompt.

## Remembering passwords (macOS Keychain)

On macOS you can opt in to storing a file's password in your login keychain:

```sh
sedit --remember secrets.enc   # prompts once, then stores the password
sedit secrets.enc              # no prompt from now on (also works with -p, --passwd)
sedit --forget secrets.enc     # remove it again
```

Nothing is stored unless you pass `--remember`. If a stored password stops
working (for example the file was re-keyed elsewhere), `sedit` removes it and
asks you to type the password. `--passwd` keeps an existing stored entry up to
date. Entries are keyed by the file's absolute path, so moving or renaming the
file means running `--remember` again. The file format is unchanged: a file can
always be opened by typing its password, on any platform.

**This weakens your security, so be aware of what you are trading.**

- The password is stored using the `/usr/bin/security` tool, which is trusted on
  the items it creates. While your login keychain is unlocked (normally whenever
  you are logged in), **any program running as your user can read the password
  without a prompt**, for example with
  `security find-generic-password -s sedit -a /path/to/file -w`. That includes
  malware and malicious scripts you run by accident.
- There is no Touch ID or password prompt per use. Doing that properly would need
  a code-signed binary with keychain entitlements, which this project
  deliberately doesn't require.
- So the practical protection of a remembered file becomes "whoever is logged in
  as you", not "whoever knows the passphrase". The encryption itself is just as
  strong; the passphrase is what's exposed.
- The login keychain file (`~/Library/Keychains/login.keychain-db`) is included
  in ordinary backups such as Time Machine, and it is protected by your login
  password.
- The password is passed to `security` through stdin, so it does not appear in the
  process list, and is base64-encoded in the keychain, which is encoding, not
  protection.

Use it for convenience on a machine you trust. Don't use it for files where the
passphrase is the only thing standing between an attacker with your account and
the contents. On other platforms `--remember` and `--forget` report that they
aren't supported.

**Cleaning up stale entries.** A stored password that no longer opens its file is
removed automatically the next time you use that file. Use
[`sedit --mv` and `sedit --rm`](command-line.md#moving-and-deleting-files) to move and delete
remembered files, and the entries follow along. But if you move or delete such a
file *with plain `mv` or `rm`*, nothing notices: its entry stays in the keychain,
still holding a working password that any program running as you can read.
`sedit` can't list its entries, so clean those up yourself:

```sh
sedit --forget /old/path/to/secrets.enc
```

This works when the path has no symlinked directories in it. Entries are stored
under the fully resolved path, which `sedit` can only work out for a file that
still exists. If it says "no stored password" for a path you know was
remembered, give it the fully resolved path instead (for example
`/private/tmp/...` rather than `/tmp/...`):

```sh
sedit --forget /private/tmp/old/secrets.enc
```

You can also delete an entry directly with the `security` tool:

```sh
security delete-generic-password -s sedit -a /resolved/path/to/secrets.enc
```

Or open **Keychain Access**, search for `sedit`, and delete the entries you no
longer need. The default password is stored under the account name `<default>`.

## Default password (macOS Keychain)

For a mix of convenience files and sensitive files, you can store one **default**
password and decide per file whether to use it:

```sh
sedit --set-default            # prompts for the default password, stores it
sedit notes.enc                # new file: asks "[d]efault or [c]ustom?"
sedit --default todo.enc       # new file with the default, no questions
sedit --custom bank.enc        # new file with its own password
sedit --passwd --custom todo.enc    # move a file off the default
sedit --forget-default
```

- **Creating a file.** If a default exists and you are at a terminal, you are
  asked whether the new file uses the default or a custom password. Without a
  terminal (a script) and without a flag, a custom password is used, so the
  default is never picked up by accident. `--default` fails if none is set.
  `--passwd` and `--encrypt` work the same way for the new password.
- **Opening a file.** `sedit` tries, in order: the file's own entry (from
  `--remember`), the default, then a prompt. A file with a custom password just
  fails the default attempt silently and prompts. That costs one extra key
  derivation (roughly a third of a second with the current settings).
- **The default is never removed automatically**, because many files rely on it.
  Per-file entries still are (see above).
- **Changing the default does not re-key your files.** Files created with the
  previous default still need the previous password. `--set-default` reminds you
  when it replaces an existing one.
- **Files don't record which kind they are.** A sensitive file whose password
  happens to equal the default will open silently, so give sensitive files a
  different password and create them with `--custom`.

The security trade-offs of the previous section apply, with a larger blast
radius: any program running as you can read the default and it opens **every**
file that uses it.
