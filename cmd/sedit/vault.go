package main

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
// The whole header (magic through nonce) is authenticated as associated
// data, so tampering with the KDF parameters is detected.
const (
	version    = 1
	saltLen    = 16
	headerLen  = 6 + 1 + 4 + 4 + 1 + saltLen + chacha20poly1305.NonceSizeX
	keyLen     = chacha20poly1305.KeySize
	defaultT   = 3
	defaultMem = 256 * 1024 // 256 MiB
	defaultThr = 4

	// Upper bounds so a malicious file can't make us allocate absurd memory.
	maxT   = 20
	maxMem = 2 * 1024 * 1024 // 2 GiB
	maxThr = 64
)

var magic = []byte("SEDIT\x00")

// ErrBadPassword is returned when authentication fails. It can't distinguish
// a wrong password from a corrupted file, by design.
var ErrBadPassword = errors.New("wrong password or corrupted file")

type kdfParams struct {
	time, memory uint32
	threads      uint8
}

var defaultKDF = kdfParams{defaultT, defaultMem, defaultThr}

func (p kdfParams) key(password, salt []byte) []byte {
	return argon2.IDKey(password, salt, p.time, p.memory, p.threads, keyLen)
}

// IsSedit reports whether data starts with the sedit magic bytes.
func IsSedit(data []byte) bool {
	return bytes.HasPrefix(data, magic)
}

// Encrypt seals plaintext under password with a fresh salt and nonce.
func Encrypt(password, plaintext []byte, p kdfParams) ([]byte, error) {
	header := make([]byte, 0, headerLen)
	header = append(header, magic...)
	header = append(header, version)
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

	key := p.key(password, salt)
	defer wipe(key)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	return aead.Seal(header, nonce, plaintext, header[:headerLen:headerLen]), nil
}

// Decrypt authenticates and opens data. A wrong password returns ErrBadPassword.
func Decrypt(password, data []byte) ([]byte, error) {
	if len(data) < headerLen+chacha20poly1305.Overhead {
		return nil, errors.New("file too short, not a sedit file")
	}
	if !bytes.Equal(data[:len(magic)], magic) {
		return nil, errors.New("not a sedit file (bad magic)")
	}
	if v := data[6]; v != version {
		return nil, fmt.Errorf("unsupported file version %d", v)
	}
	p := kdfParams{
		time:    binary.BigEndian.Uint32(data[7:11]),
		memory:  binary.BigEndian.Uint32(data[11:15]),
		threads: data[15],
	}
	if p.time == 0 || p.time > maxT || p.memory < 8 || p.memory > maxMem || p.threads == 0 || p.threads > maxThr {
		return nil, errors.New("invalid KDF parameters in header")
	}
	salt := data[16 : 16+saltLen]
	nonce := data[16+saltLen : headerLen]

	key := p.key(password, salt)
	defer wipe(key)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	pt, err := aead.Open(nil, nonce, data[headerLen:], data[:headerLen])
	if err != nil {
		return nil, ErrBadPassword
	}
	return pt, nil
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
