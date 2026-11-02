// Package backup is what the control plane and the agent share for server backups:
// the archive encryption format and a small S3 client.
package backup

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
)

// Archive format: magic | 32-byte salt | chunks. Each chunk is up to 64 KiB of plaintext sealed with
// AES-256-GCM under HKDF(key, salt); its nonce is the chunk counter plus a "last chunk" flag, so
// reordering, truncation and appending are detected (the STREAM construction). A fresh salt per
// archive means nonces never repeat under one key.
const chunkSize = 64 << 10

var magic = []byte("ORBITBK1")

var ErrCorrupt = errors.New("backup archive is corrupt, was tampered with, or the key is wrong")

func fileAEAD(key, salt []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("backup key must be 32 bytes")
	}
	k, err := hkdf.Key(sha256.New, key, salt, "orbit server backup v1", 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func chunkNonce(n uint64, last bool) []byte {
	nonce := make([]byte, 12)
	binary.BigEndian.PutUint64(nonce[3:11], n)
	if last {
		nonce[11] = 1
	}
	return nonce
}

type encWriter struct {
	w    io.Writer
	aead cipher.AEAD
	buf  []byte
	n    uint64
}

// Encrypt returns a writer that encrypts into w. Close must be called to write the final chunk.
func Encrypt(w io.Writer, key []byte) (io.WriteCloser, error) {
	salt := make([]byte, 32)
	rand.Read(salt)
	aead, err := fileAEAD(key, salt)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(append(append([]byte{}, magic...), salt...)); err != nil {
		return nil, err
	}
	return &encWriter{w: w, aead: aead, buf: make([]byte, 0, chunkSize)}, nil
}

func (e *encWriter) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		// a full chunk is sealed only once more data arrives, so the last chunk is always flagged
		if len(e.buf) == chunkSize {
			if err := e.seal(false); err != nil {
				return total, err
			}
		}
		k := copy(e.buf[len(e.buf):chunkSize], p)
		e.buf = e.buf[:len(e.buf)+k]
		p, total = p[k:], total+k
	}
	return total, nil
}

func (e *encWriter) seal(last bool) error {
	_, err := e.w.Write(e.aead.Seal(nil, chunkNonce(e.n, last), e.buf, nil))
	e.n++
	e.buf = e.buf[:0]
	return err
}

func (e *encWriter) Close() error { return e.seal(true) }

type decReader struct {
	r    *bufio.Reader
	aead cipher.AEAD
	ct   []byte
	buf  []byte
	n    uint64
	done bool
}

// Decrypt returns a reader of the plaintext. Any tampering surfaces as ErrCorrupt before the
// affected bytes are returned.
func Decrypt(r io.Reader, key []byte) (io.Reader, error) {
	head := make([]byte, len(magic)+32)
	if _, err := io.ReadFull(r, head); err != nil || string(head[:len(magic)]) != string(magic) {
		return nil, errors.New("not an Orbit backup archive")
	}
	aead, err := fileAEAD(key, head[len(magic):])
	if err != nil {
		return nil, err
	}
	return &decReader{r: bufio.NewReader(r), aead: aead, ct: make([]byte, chunkSize+aead.Overhead())}, nil
}

func (d *decReader) Read(p []byte) (int, error) {
	for len(d.buf) == 0 {
		if d.done {
			return 0, io.EOF
		}
		if err := d.next(); err != nil {
			return 0, err
		}
	}
	k := copy(p, d.buf)
	d.buf = d.buf[k:]
	return k, nil
}

func (d *decReader) next() error {
	n, err := io.ReadFull(d.r, d.ct)
	last := false
	switch {
	case err == io.EOF || err == io.ErrUnexpectedEOF:
		last = true
	case err != nil:
		return err
	default:
		_, perr := d.r.Peek(1)
		last = perr == io.EOF
	}
	if n == 0 {
		return ErrCorrupt // the final chunk is always present, even when empty
	}
	pt, err := d.aead.Open(nil, chunkNonce(d.n, last), d.ct[:n], nil)
	if err != nil {
		return ErrCorrupt
	}
	d.n++
	d.done = last
	d.buf = pt
	return nil
}
