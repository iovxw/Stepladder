package aestcp

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	mrand "math/rand"
	"net"
	"time"
)

var r = mrand.New(mrand.NewSource(time.Now().UnixNano()))

func xorByteByKey(b byte, key []byte) byte {
	for _, k := range key {
		b = b ^ k
	}
	return b
}

func newRandBytes(l int) []byte {
	b := make([]byte, l)
	rand.Read(b)
	return b
}

func toBytes32(b []byte) *[32]byte {
	if len(b) != 32 {
		panic("len(b) != 32")
	}
	var r = new([32]byte)
	copy(r[:], b)
	return r
}

func readPlaceholder(conn net.Conn, key []byte) (int, error) {
	buf := make([]byte, 1)
	n, err := conn.Read(buf)
	if err != nil {
		return 0, err
	}

	length := xorByteByKey(buf[0], key)
	if length != 0 {
		_, err = conn.Read(make([]byte, length))
		if err != nil {
			return 0, err
		}
	}
	return n + int(length), nil
}

func Dial(network, address string, key []byte) (net.Conn, error) {
	conn, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}

	privateKey, publicKey, err := generateKey(rand.Reader)
	if err != nil {
		return nil, err
	}

	l1 := r.Intn(32)
	r1 := newRandBytes(l1)
	rHead := append([]byte{xorByteByKey(byte(l1), key)}, r1...)
	l2 := r.Intn(32)
	r2 := newRandBytes(l2)
	rTail := append([]byte{xorByteByKey(byte(l2), key)}, r2...)

	/*
		+------+----------+--------+------+----------+
		| RLEN | RAND     | PubKey | RLEN | RAND     |
		+--------------------------------------------+
		| 1    | Variable | 32     | 1    | Variable |
		+------+----------+--------+------+----------+
	*/
	buffer := bytes.NewBuffer(rHead)
	buffer.Write(publicKey[:])
	buffer.Write(rTail)

	_, err = conn.Write(buffer.Bytes())
	if err != nil {
		return nil, err
	}

	/*
		+------+----------+--------+------+------+----------+
		| RLEN | RAND     | PubKey | HASH | RLEN | RAND     |
		+---------------------------------------------------+
		| 1    | Variable | 32     | 32   | 1    | Variable |
		+------+----------+--------+------+------+----------+
	*/
	_, err = readPlaceholder(conn, key)
	if err != nil {
		return nil, err
	}

	buf := make([]byte, 32+32)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	if n != 32+32 {
		return nil, io.ErrUnexpectedEOF
	}

	serverPubKey, hash := toBytes32(buf[:32]), toBytes32(buf[32:])

	_, err = readPlaceholder(conn, key)
	if err != nil {
		return nil, err
	}

	result := generateSharedSecret(privateKey, serverPubKey)
	aesKey, iv1, iv2 := result[:], result[:16], result[16:]

	if sha256.Sum256(append(key, result[:]...)) != *hash {
		return nil, errors.New("Hash values do not match")
	}

	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, err
	}

	readStream := cipher.NewCTR(block, iv1)
	writeStream := cipher.NewCTR(block, iv2)

	return &AESConn{
		Conn:         conn,
		b:            block,
		StreamReader: &cipher.StreamReader{S: readStream, R: conn},
		StreamWriter: &cipher.StreamWriter{S: writeStream, W: conn},
	}, nil
}

type AESConn struct {
	net.Conn
	*cipher.StreamReader
	*cipher.StreamWriter
	b cipher.Block
}

func (c *AESConn) Close() error {
	return c.Conn.Close()
}

func (c *AESConn) Read(dst []byte) (int, error) {
	return c.StreamReader.Read(dst)
}

func (c *AESConn) Write(src []byte) (int, error) {
	return c.StreamWriter.Write(src)
}

func Listen(network, laddr string, key []byte) (net.Listener, error) {
	ln, err := net.Listen(network, laddr)
	if err != nil {
		return nil, err
	}

	return &AESListener{ln, key}, nil
}

type AESListener struct {
	net.Listener
	key []byte
}

func (l *AESListener) Accept() (c net.Conn, err error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	/*
		+------+----------+--------+------+----------+
		| RLEN | RAND     | PubKey | RLEN | RAND     |
		+--------------------------------------------+
		| 1    | Variable | 32     | 1    | Variable |
		+------+----------+--------+------+----------+
	*/
	_, err = readPlaceholder(conn, l.key)
	if err != nil {
		return nil, err
	}

	buf := make([]byte, 32)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	if n != 32 {
		return nil, io.ErrUnexpectedEOF
	}
	clientPubKey := toBytes32(buf)

	_, err = readPlaceholder(conn, l.key)
	if err != nil {
		return nil, err
	}

	privateKey, publicKey, err := generateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	result := generateSharedSecret(privateKey, clientPubKey)
	aesKey, iv1, iv2 := result[:], result[:16], result[16:]

	hash := sha256.Sum256(append(l.key, result[:]...))

	l1 := r.Intn(32)
	r1 := newRandBytes(l1)
	rHead := append([]byte{xorByteByKey(byte(l1), l.key)}, r1...)
	l2 := r.Intn(32)
	r2 := newRandBytes(l2)
	rTail := append([]byte{xorByteByKey(byte(l2), l.key)}, r2...)

	/*
		+------+----------+--------+------+------+----------+
		| RLEN | RAND     | PubKey | HASH | RLEN | RAND     |
		+---------------------------------------------------+
		| 1    | Variable | 32     | 32   | 1    | Variable |
		+------+----------+--------+------+------+----------+
	*/
	buffer := bytes.NewBuffer(rHead)
	buffer.Write(publicKey[:])
	buffer.Write(hash[:])
	buffer.Write(rTail)

	_, err = conn.Write(buffer.Bytes())
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, err
	}

	readStream := cipher.NewCTR(block, iv2)
	writeStream := cipher.NewCTR(block, iv1)

	return &AESConn{
		Conn:         conn,
		b:            block,
		StreamReader: &cipher.StreamReader{S: readStream, R: conn},
		StreamWriter: &cipher.StreamWriter{S: writeStream, W: conn},
	}, nil
}
