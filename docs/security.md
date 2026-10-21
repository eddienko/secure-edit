# Security and file format

What `sedit` protects, what it does not, and how the files are laid out.

## Limitations

Read these before relying on `sedit`. It protects data **at rest**. It does not
defend against malware or an attacker on your machine while the file is open.

- **Plaintext temp file.** While you edit, the plaintext sits in a `0700` temp
  directory. It is zeroed and deleted afterwards, but on macOS the temp
  directory is on disk, not RAM, so the data may have reached the SSD. Zeroing
  can't guarantee otherwise. Using a RAM disk is a stronger option.
- **Other editors may leak.** Leak protection is built in for vi, vim and nvim
  only. Editors such as VS Code, Emacs and nano may write backup, swap or
  recovery files, so configure them yourself. Graphical editors are the worst
  case: VS Code, for example, keeps its own Local History and hot-exit backups,
  which can hold plaintext copies of every version you save. That is why `sedit`
  ignores them unless you set `SEDIT_EDITOR`. A terminal editor with leak
  protection (vim) is the safer choice for secrets.
- **Memory is not locked or reliably wiped.** Go's garbage collector can leave
  copies of the password and plaintext in memory, and memory may be swapped.
- **Your passphrase is the weak point.** Argon2id slows brute force, but a weak
  passphrase is still weak.
- **No integrity beyond one file.** A wrong password and a corrupted file give
  the same error (`wrong password or corrupted file`), by design.
- **`FILE.bak` keeps the old password.** After `--passwd`, an existing `.bak` is
  still encrypted with the *old* password. `sedit` warns you but doesn't delete
  it. Remove it yourself if the old password was compromised.
- **Converted plaintext isn't erased.** After `--encrypt`, the original plaintext
  may remain in freed disk blocks, backups, Time Machine snapshots or editor
  files. Treat the old contents as exposed, and rotate any credentials in it.
- **Remembered and default passwords are readable by any process running as
  you.** See [Remembering passwords](keychain.md#remembering-passwords-macos-keychain) and
  [Default password](keychain.md#default-password-macos-keychain). A leaked default opens
  every file that uses it. Only use these where that is acceptable.
- **Single backup generation.** Only the immediately preceding version is kept.
- **Lock file is advisory and local.** The lock is an `flock` on `.FILE.lock`,
  which is left on disk (empty) after exit. It is released automatically if
  `sedit` crashes. It may be unreliable on some network filesystems such as NFS,
  and it does not protect copies of the file elsewhere. `sedit -p` doesn't take
  the lock, which is safe because saves are atomic renames.
- **Unix only.** The lock uses `syscall.Flock`, so it won't build on Windows.
- **No recovery.** There is no password reset. Lose the passphrase and the data
  is gone.
- **Not independently audited.** It uses standard primitives from
  `golang.org/x/crypto` and no custom cryptography, but the program as a whole
  hasn't had a security review.

## File format

All integers are big endian.

| Field      | Size | Notes                              |
|------------|------|------------------------------------|
| magic      | 6    | `SEDIT\0`                          |
| version    | 1    | `1`, or `2` if there is a title or description |
| time       | 4    | Argon2id iterations                |
| memory     | 4    | Argon2id memory in KiB             |
| threads    | 1    | Argon2id parallelism               |
| salt       | 16   | random, fresh on every save        |
| nonce      | 24   | random, fresh on every save        |
| title length | 2  | version 2 only                     |
| description length | 2 | version 2 only                |
| title, description | n | version 2 only, UTF-8, in the clear |
| ciphertext | n+16 | XChaCha20-Poly1305, includes tag   |

The header (magic through the description, if there is one) is passed as
associated data, so a change to the KDF parameters, the title or the description
is detected. The parameters and the lengths are bounded on read, so a hostile file
can't force huge allocations.
