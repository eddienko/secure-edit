# Roadmap

Ideas for later. Nothing here is planned or promised; it is a place to keep the
thinking so that it isn't lost. Each item says what it is, why, and what is still
undecided.

## Recovery key (a way back from a forgotten password)

**Why.** There is no password reset: lose the passphrase and the file is gone
(see [security.md](security.md)). That is a strong guarantee, but some people
would rather have a way back. It must be **opt-in**: any recovery path is a second
way into every file it covers.

**Not a master password.** A master password would have to be typed whenever a
file is created, so the file can be sealed to it, and one weak master would expose
everything. A recovery *key* is a public/private pair: the public half is
configured once and used silently; the private half is kept offline and used only
when a password is lost.

**Design (preferred): a recovery slot in the file header, format version 3.**
- Each file gets a random data key that encrypts the text. The header holds two
  ways to that key, like the key slots of LUKS: the **password slot** (the same
  Argon2id derivation as today, wrapping the data key) and, if a recovery key is
  configured, a **recovery slot**: the data key encrypted to the recovery public
  key with the age library we already depend on, so no new cryptography.
- The whole header, slots included, stays authenticated as associated data.
- The recovery slot needs only the public key, so it is added on every save with no
  prompt. A file made before the key was configured gets its slot at its next save,
  and `sedit` says so. A `.bak` has its own header, so it is recoverable too.
- `sedit --recover FILE -i recovery.key` opens a file with the recovery key and
  asks for a *new* password. The text is untouched and the recovery slot stays.
  `--passwd` only re-wraps the password slot.
- Setup: `sedit --keygen -o recovery.key` already makes the key pair (`--pq` for a
  post-quantum one). The public key would be configured with something like
  `SEDIT_RECOVERY_KEY=age1…`. The private key lives offline, never next to the
  files, ideally protected by a passphrase of its own.
- `sedit --info FILE` and the interface could say "recovery key: yes", and the
  interface could mark files that don't have one yet.

**Costs.**
- Files with a recovery slot (like labelled files) can't be opened by an older
  `sedit`, and recovery needs `sedit`, not plain `age`.
- If the recovery key leaks, every file sealed to it leaks. Rotating the key takes
  effect per file at its next save; sealing a whole folder to a new key needs each
  file's password.
- The header shows that a recovery slot exists, though not whose key it is.

**Cheaper alternative (no format change): an automatic recovery copy.** On each
save, also write `FILE.recovery`, an age-encrypted copy of the text to the recovery
public key. It could be opened with plain `age`, and needs no new format. But it is
a second ciphertext of the text beside every file, which must move and be deleted
with the file like `.bak` (in `fileops`) and goes stale if the file is changed
outside `sedit`. Worth trying first if a format change feels too big.
Today's manual version of the same idea: `sedit --share FILE --to <your age key>`.

**Tests it would need.** The round trip; a wrong key failing; a tampered slot
detected; a save adding the slot to an older file; `--passwd` keeping it; files of
versions 1 and 2 still opening unchanged.

**Undecided.**
- The slot design or the recovery copy first.
- Where the public key lives: an environment variable, a small config file, or both.
- Whether the interface should mark files without a slot, or stay quiet.

## Interface

- **A list widget of our own**, replacing the tree widget. It would let a titled
  *folder* show its name under its title in the two-row view (the tree widget lists
  a folder's children right after it, so a name row would vanish when the folder is
  closed), fix the name row being cut off at the bottom edge, and make a
  resizable split and a right-aligned names column easier.
- **Remember the view.** `v` starts on titles only each time. A setting such as
  `SEDIT_LIST=two`, or a small config file, could keep the choice. (`Options.TwoLine`
  already exists in the code.)
- **A resizable split** (`<` and `>`), and a wider default list when titles are long.
- **Copy one line** of the text, not all of it, so that a credentials file puts one
  value on the clipboard. `y` would keep copying everything.
- **Lock when the terminal loses focus**, which is stronger than the idle timer, and a
  shorter blur of the text after a few seconds without a key.
- **Refresh by itself** when the folder changes on disk, instead of with `R`.
- **Several files at once:** mark files to move or delete together.
- **More of the command line in the interface:** encrypt a plain file in place, import an
  age file, share, restore the previous version from `.bak`, duplicate a file.
- **Narrow terminals:** under about 60 columns show one pane at a time.
- **Remember** the last folder and which folders were open.
- **An `--ascii` mode** for fonts where `▪ ▫ ○ ·` render badly, and an opt-in mouse
  (it switches off the terminal's own text selection).
- **A folder password or session password** that is asked once for a whole folder was
  considered and set aside: with passwords in the Keychain the folder is only as
  protected as your login session. An opt-in `--ask`, which ignores the Keychain and
  asks for the password at launch, was the shape of it if it comes back.

## Folders from the command line

Folders can be made, retitled, renamed, moved and deleted only in the interface.
`sedit --mv` could accept a folder and `sedit --rm -r` could delete one, using the
same `fileops` code.

## Release and packaging

- A Homebrew formula (a single `sedit` binary now makes this simple).
- Code-signing and notarising the macOS binaries.
