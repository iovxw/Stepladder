package aestcp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"io"
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

	return &AESConn{
		Conn: conn,
		b:    block,
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
	if c.StreamReader == nil {
		// 未初始化，读取IV并初始化
		/*
			+---------------+----------+
			| IV            | DATA     |
			+---------------+----------+
			| aes.BlockSize | Variable |
			+---------------+----------+

			- IV: AES 加密后的 IV
			- DATA: 使用 IV 生成 Stream 加密后的数据
		*/
		iv := make([]byte, aes.BlockSize)
		n, err := c.Conn.Read(iv)
		if err != nil {
			return 0, err
		}
		if n != aes.BlockSize {
			// TODO: errors.New("err msg")
			return 0, err
		}
		c.b.Decrypt(iv, iv)

		stream := cipher.NewCTR(c.b, iv[:])
		c.StreamReader = &cipher.StreamReader{S: stream, R: c.Conn}
	}
	return c.StreamReader.Read(dst)
}

func (c *AESConn) Write(src []byte) (int, error) {
	if c.StreamWriter == nil {
		// 未初始化，生成IV并初始化
		/*
			+---------------+----------+
			| IV            | DATA     |
			+---------------+----------+
			| aes.BlockSize | Variable |
			+---------------+----------+

			- IV: AES 加密后的 IV
			- DATA: 使用 IV 生成 Stream 加密后的数据
		*/
		iv := make([]byte, aes.BlockSize)
		_, err := io.ReadFull(rand.Reader, iv)
		if err != nil {
			return 0, err
		}

		stream := cipher.NewCTR(c.b, iv)
		c.StreamWriter = &cipher.StreamWriter{S: stream, W: c.Conn}

		c.b.Encrypt(iv, iv)
		src = append(iv, src...)
		stream.XORKeyStream(src[aes.BlockSize:], src[aes.BlockSize:])
		n, err := c.Conn.Write(src)
		if err != nil {
			return 0, err
		}
		return n - aes.BlockSize, nil
	}
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

	return &AESListener{ln, block, key}, nil
}

type AESListener struct {
	net.Listener
	block cipher.Block
	key   []byte
}

func (l *AESListener) Accept() (c net.Conn, err error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(l.key)
	if err != nil {
		return nil, err
	}

	return &AESConn{
		Conn: conn,
		b:    block,
	}, nil
}
