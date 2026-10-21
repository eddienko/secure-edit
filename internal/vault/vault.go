// Package vault implements the sedit file format (authenticated encryption with
// a key derived from a password), the per-file edit lock, and the atomic file
// operations that go with them. It knows nothing about prompts, editors or the
// Keychain.
package vault

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

// File layout (all integers big endian):
//
//	magic    6  "SEDIT\x00"
//	version  1
//	time     4  argon2id iterations
//	memory   4  argon2id memory in KiB
//	threads  1  argon2id parallelism
//	salt    16
//	nonce   24  XChaCha20-Poly1305
//	ciphertext + 16-byte tag
//
// Version 2 adds an optional title and description, readable without the
// password, between the nonce and the ciphertext:
//
//	title length        2
//	description length  2
//	title, description  UTF-8
//
// A file without them is written as version 1, so only files that use the
// feature need a sedit that knows version 2.
//
// The whole header (magic through the description) is authenticated as
// associated data, so tampering with the KDF parameters or the labels is
// detected.
const (
	version     = 1 // no labels
	versionMeta = 2 // with a title and description
	saltLen     = 16
	headerLen   = 6 + 1 + 4 + 4 + 1 + saltLen + chacha20poly1305.NonceSizeX
	metaLenLen  = 4 // the two length fields of version 2
	keyLen      = chacha20poly1305.KeySize
	defaultT    = 3
	defaultMem  = 256 * 1024 // 256 MiB
	defaultThr  = 4

	// Upper bounds so a malicious file can't make us allocate absurd memory.
	maxT   = 20
	maxMem = 2 * 1024 * 1024 // 2 GiB
	maxThr = 64
)

var magic = []byte("SEDIT\x00")

// ErrBadPassword is returned when authentication fails. It can't distinguish
// a wrong password from a corrupted file, by design.
var ErrBadPassword = errors.New("wrong password or corrupted file")

// KDFParams are the Argon2id parameters stored in a file's header.
type KDFParams struct {
	time, memory uint32
	threads      uint8
}

// NewKDFParams returns Argon2id parameters: iterations, memory in KiB, and
// parallelism. Files record their own parameters, so a cheap setting is useful
// for tests; use DefaultKDF for real data.
func NewKDFParams(time, memoryKiB uint32, threads uint8) KDFParams {
	return KDFParams{time, memoryKiB, threads}
}

// DefaultKDF is the Argon2id cost used for new and re-encrypted files.
var DefaultKDF = KDFParams{defaultT, defaultMem, defaultThr}

func (p KDFParams) key(password, salt []byte) []byte {
	return argon2.IDKey(password, salt, p.time, p.memory, p.threads, keyLen)
}

// IsSedit reports whether data starts with the sedit magic bytes.
func IsSedit(data []byte) bool {
	return bytes.HasPrefix(data, magic)
}

// Encrypt seals plaintext under password with a fresh salt and nonce, and no
// title or description.
func Encrypt(password, plaintext []byte, p KDFParams) ([]byte, error) {
	return EncryptMeta(password, plaintext, p, Meta{})
}

// EncryptMeta is Encrypt with a title and description stored in the header. If
// both are empty the file is an ordinary version 1 file.
func EncryptMeta(password, plaintext []byte, p KDFParams, m Meta) ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	ver := byte(version)
	if !m.IsZero() {
		ver = versionMeta
	}
	header := make([]byte, 0, headerLen+metaLenLen+len(m.Title)+len(m.Description))
	header = append(header, magic...)
	header = append(header, ver)
	header = binary.BigEndian.AppendUint32(header, p.time)
	header = binary.BigEndian.AppendUint32(header, p.memory)
	header = append(header, p.threads)

	salt := make([]byte, saltLen)
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	header = append(header, salt...)
	header = append(header, nonce...)
	if ver == versionMeta {
		header = binary.BigEndian.AppendUint16(header, uint16(len(m.Title)))
		header = binary.BigEndian.AppendUint16(header, uint16(len(m.Description)))
		header = append(header, m.Title...)
		header = append(header, m.Description...)
	}

	key := p.key(password, salt)
	defer Wipe(key)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	return aead.Seal(header, nonce, plaintext, header[:len(header):len(header)]), nil
}

// header is a parsed file header.
type header struct {
	params KDFParams
	salt   []byte
	nonce  []byte
	meta   Meta
	length int // bytes of data taken by the header, which is also the associated data
}

// parseHeader reads the header of data. Nothing in it is trusted until the
// ciphertext has been opened.
func parseHeader(data []byte) (header, error) {
	var h header
	if len(data) < headerLen {
		return h, errors.New("file too short, not a sedit file")
	}
	if !bytes.Equal(data[:len(magic)], magic) {
		return h, errors.New("not a sedit file (bad magic)")
	}
	v := data[6]
	if v != version && v != versionMeta {
		return h, fmt.Errorf("unsupported file version %d", v)
	}
	h.params = KDFParams{
		time:    binary.BigEndian.Uint32(data[7:11]),
		memory:  binary.BigEndian.Uint32(data[11:15]),
		threads: data[15],
	}
	p := h.params
	if p.time == 0 || p.time > maxT || p.memory < 8 || p.memory > maxMem || p.threads == 0 || p.threads > maxThr {
		return h, errors.New("invalid KDF parameters in header")
	}
	h.salt = data[16 : 16+saltLen]
	h.nonce = data[16+saltLen : headerLen]
	h.length = headerLen
	if v == versionMeta {
		if len(data) < headerLen+metaLenLen {
			return h, errors.New("file too short, not a sedit file")
		}
		tl := int(binary.BigEndian.Uint16(data[headerLen:]))
		dl := int(binary.BigEndian.Uint16(data[headerLen+2:]))
		if tl > MaxTitle || dl > MaxDescription {
			return h, errors.New("invalid title or description in header")
		}
		h.length = headerLen + metaLenLen + tl + dl
		if len(data) < h.length {
			return h, errors.New("file too short, not a sedit file")
		}
		body := data[headerLen+metaLenLen : h.length]
		h.meta = Meta{Title: string(body[:tl]), Description: string(body[tl:])}
	}
	if len(data) < h.length+chacha20poly1305.Overhead {
		return h, errors.New("file too short, not a sedit file")
	}
	return h, nil
}

// ReadMeta returns the title and description in data's header. It needs no
// password, and for the same reason it can't vouch for them: they are only
// known to be genuine once the file has been opened with Decrypt. A file
// without them gives an empty Meta.
func ReadMeta(data []byte) (Meta, error) {
	h, err := parseHeader(data)
	if err != nil {
		return Meta{}, err
	}
	return h.meta, nil
}

// Decrypt authenticates and opens data. A wrong password returns ErrBadPassword.
func Decrypt(password, data []byte) ([]byte, error) {
	h, err := parseHeader(data)
	if err != nil {
		return nil, err
	}
	key := h.params.key(password, h.salt)
	defer Wipe(key)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	pt, err := aead.Open(nil, h.nonce, data[h.length:], data[:h.length])
	if err != nil {
		return nil, ErrBadPassword
	}
	return pt, nil
}

// Wipe zeroes b. Use it on passwords and plaintext when you are done with them.
func Wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
