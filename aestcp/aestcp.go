package aestcp

import (
	"crypto/aes"
	"crypto/cipher"
	"net"
)

func Dial(network, address string, key []byte) (net.Conn, error) {
	conn, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	var iv [aes.BlockSize]byte
	stream := cipher.NewOFB(block, iv[:])

	reader := &cipher.StreamReader{S: stream, R: conn}
	writer := &cipher.StreamWriter{S: stream, W: conn}

	return &AESConn{conn, reader, writer}, nil
}

type AESConn struct {
	net.Conn
	*cipher.StreamReader
	*cipher.StreamWriter
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

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	return &AESListener{ln, block}, nil
}

type AESListener struct {
	net.Listener
	block cipher.Block
}

func (l *AESListener) Accept() (c net.Conn, err error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	var iv [aes.BlockSize]byte
	stream := cipher.NewOFB(l.block, iv[:])

	reader := &cipher.StreamReader{S: stream, R: conn}
	writer := &cipher.StreamWriter{S: stream, W: conn}

	return &AESConn{conn, reader, writer}, nil
}
