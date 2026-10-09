# sedit

Edit a passphrase-encrypted text file with your normal editor.

`sedit` is a stronger take on `vim -x`: it uses authenticated encryption, so a
wrong password is rejected **before** the editor opens, and any tampering with
the file is detected.

## Features

- **Strong encryption:** XChaCha20-Poly1305 with a key derived from your
  passphrase by Argon2id (3 passes, 256 MiB, 4 threads).
- **Wrong password fails:** the file is authenticated and decrypted before the
  editor launches. No more editing garbage.
- **Tamper detection:** the whole file header, including the KDF parameters, is
  authenticated.
- **Your editor:** uses `$VISUAL`, then `$EDITOR`, then `vi`. For vi, vim and
  nvim it disables swap, backup, undo and viminfo files so plaintext isn't
  leaked.
- **Safe saves:** written atomically (temp file, fsync, rename). If the editor
  exits with an error, or you make no changes, the file is left untouched.
- **Previous version kept:** each save keeps the old version, still encrypted,
  as `FILE.bak`.
- **Concurrent-edit protection:** a lock prevents two `sedit` processes from
  editing the same file at once.

## Install

Requires Go 1.25 or newer.

```sh
git clone https://github.com/eddienko/secure-edit.git
cd secure-edit
go build -o sedit .
# optionally: mv sedit /usr/local/bin/
```

## Usage

```
sedit FILE                 edit FILE (created if it doesn't exist or is empty)
sedit -p FILE              decrypt FILE to stdout
sedit --passwd FILE        change FILE's password
sedit --encrypt [-y] FILE  encrypt an existing plaintext FILE in place
```

```sh
sedit secrets.enc            # prompts for a password, opens $EDITOR
sedit -p secrets.enc | grep api
sedit --passwd secrets.enc
```

For a new file you are asked for the password twice. An empty password is
rejected.

### Converting an existing plaintext file

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

### Restoring the previous version

```sh
sedit -p secrets.enc.bak     # inspect it (same password)
mv secrets.enc.bak secrets.enc
```

Only one previous version is kept; each save overwrites `FILE.bak`.

### Scripting

When stdin is not a terminal, the password is read as a single line from stdin:

```sh
echo "$PASSWORD" | sedit -p secrets.enc
```

No confirmation prompt is shown in that mode. Beware that passwords in shell
variables, history and process lists are visible to other tools.

## File format

All integers are big endian.

| Field      | Size | Notes                              |
|------------|------|------------------------------------|
| magic      | 6    | `SEDIT\0`                          |
| version    | 1    | currently `1`                      |
| time       | 4    | Argon2id iterations                |
| memory     | 4    | Argon2id memory in KiB             |
| threads    | 1    | Argon2id parallelism               |
| salt       | 16   | random, fresh on every save        |
| nonce      | 24   | random, fresh on every save        |
| ciphertext | n+16 | XChaCha20-Poly1305, includes tag   |

The header (magic through nonce) is passed as associated data. KDF parameters in
a file are bounded on read, so a hostile file can't force huge allocations.

## Limitations

Read these before relying on `sedit`. It protects data **at rest**. It does not
defend against malware or an attacker on your machine while the file is open.

- **Plaintext temp file.** While you edit, the plaintext sits in a `0700` temp
  directory. It is zeroed and deleted afterwards, but on macOS the temp
  directory is on disk, not RAM, so the data may have reached the SSD. Zeroing
  can't guarantee otherwise. Using a RAM disk is a stronger option.
- **Other editors may leak.** Leak protection is built in for vi, vim and nvim
  only. Editors such as VS Code, Emacs and nano may write backup, swap or
  recovery files, so configure them yourself.
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

## Development

```sh
go vet ./...
go test ./...
```

## License

[MIT](LICENSE) © 2026 Eduardo Gonzalez Solares
