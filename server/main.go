/*/ // ===========================================================================
 *  The MIT License (MIT)
 *
 *  Copyright (c) 2015 Bluek404
 *
 *  Permission is hereby granted, free of charge, to any person obtaining a copy
 *  of this software and associated documentation files (the "Software"), to deal
 *  in the Software without restriction, including without limitation the rights
 *  to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 *  copies of the Software, and to permit persons to whom the Software is
 *  furnished to do so, subject to the following conditions:
 *
 *  The above copyright notice and this permission notice shall be included in all
 *  copies or substantial portions of the Software.
 *
 *  THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 *  IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 *  FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 *  AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 *  LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 *  OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 *  SOFTWARE.
/*/ // ===========================================================================

package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/Bluek404/Stepladder/aestcp"
)

const VERSION = "3.3.0"

func main() {
	var key string
	var port int
	flag.StringVar(&key, "k", "eGauUecvzS05U5DIsxAN4n2hadmRTZGB", "key")
	flag.IntVar(&port, "p", 8081, "port")
	flag.Parse()

	keyB := []byte(key)
	switch len(keyB) {
	case 16, 32, 64:
		break
	default:
		log.Println("KEY 长度必须为 16、32 或者 64")
		os.Exit(1)
	}

	// 监听端口
	ln, err := aestcp.Listen("tcp", ":"+strconv.Itoa(port), keyB)
	if err != nil {
		log.Println(err)
		os.Exit(1)
	}
	defer ln.Close()

	// 加载完成后输出配置信息
	log.Println("|>>>>>>>>>>>>>>>|<<<<<<<<<<<<<<<|")
	log.Println("Version:", VERSION)
	log.Println("Port:", port)
	log.Println("Key:", key)
	log.Println("|>>>>>>>>>>>>>>>|<<<<<<<<<<<<<<<|")

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Println(err)
			continue
		}
		go handleConnection(conn)
	}
}

func timeoutLoop(t time.Duration, do func(), alive, exit chan bool) {
	select {
	case <-alive:
		timeoutLoop(t, do, alive, exit)
	case <-exit:
	case <-time.After(t):
		do()
	}
}

func newTimeouter(t time.Duration, do func()) (chan<- bool, chan<- bool) {
	alive := make(chan bool, 1)
	exit := make(chan bool, 1)
	go timeoutLoop(t, do, alive, exit)

	return alive, exit
}

func handleConnection(conn net.Conn) {
	log.Println("[+]", conn.RemoteAddr())
	defer log.Println("[-]", conn.RemoteAddr())

	/*
		两种可能的请求:

		+-----+
		| CMD |
		+-----+
		| 1   |
		+-----+

		- CMD: 协议类型。0为TCP，1为UDP

		+-----+----------+----------+------+
		| CMD | HOST LEN | HOST     | PORT |
		+-----+----------+----------+------+
		| 1   | 1        | Variable | 2    |
		+-----+----------+----------+------+

		- CMD: 协议类型。0为TCP，1为UDP
		- HOST LEN: 目标地址的长度
		- HOST: 目标地址，IPv[4|6]或者域名
		- PORT: 目标端口，使用大端字节序，uint16
	*/

	// 读取协议类型
	buf := make([]byte, 1)
	_, err := conn.Read(buf)
	if err != nil {
		log.Println(err)
		conn.Close()
		return
	}
	if buf[0] == 0 {
		proxyTCP(conn)
	} else {
		proxyUDP(conn)
	}
}

func proxyTCP(conn net.Conn) {
	// 读取host长度
	buf := make([]byte, 1)
	_, err := conn.Read(buf)
	if err != nil {
		log.Println(err)
		conn.Close()
		return
	}
	hostLen := int(buf[0])
	// 读取host
	buf = make([]byte, hostLen)
	n, err := conn.Read(buf)
	if err != nil {
		log.Println(err)
		conn.Close()
		return
	}
	if n != hostLen {
		log.Println("host长度错误")
		return
	}
	host := string(buf)
	// 读取port
	var port uint16
	err = binary.Read(conn, binary.BigEndian, &port)
	if err != nil {
		log.Println(err)
		conn.Close()
		return
	}

	url := host + ":" + strconv.Itoa(int(port))

	// 输出信息
	log.Println(conn.RemoteAddr(), "==tcp==", url, "[+]")

	/*
		+------+
		| CODE |
		+------+
		| 1    |
		+------+

		- CODE: 状态码。0为成功，2为session无效，[1|3-5]为socks5相应状态码
	*/
	// connect
	pconn, err := net.Dial("tcp", url)
	if err != nil {
		log.Println(err)
		log.Println(conn.RemoteAddr(), "==tcp==", url, "[×]")
		// 给客户端返回错误信息
		conn.Write([]byte{3})
		conn.Close()
		return
	}

	_, err = conn.Write([]byte{0})
	if err != nil {
		log.Println(err)
		conn.Close()
		pconn.Close()
		return
	}

	wg := new(sync.WaitGroup)
	wg.Add(2)
	go func() {
		Copy(pconn, conn)
		conn.Close()
		pconn.Close()
		wg.Done()
	}()
	go func() {
		Copy(conn, pconn)
		conn.Close()
		pconn.Close()
		wg.Done()
	}()

	wg.Wait()
	log.Println(conn.RemoteAddr(), "==tcp==", url, "[√]")
}

func proxyUDP(conn net.Conn) {
	pconn, err := net.ListenUDP("udp", nil)
	if err != nil {
		log.Println(err)
		conn.Write([]byte{1})
		conn.Close()
		return
	}
	_, err = conn.Write([]byte{0})
	if err != nil {
		log.Println(err)
		pconn.Close()
		conn.Close()
		return
	}
	log.Println(conn.RemoteAddr(), "==udp==", "ALL", "[+]")

	alive, exit := newTimeouter(time.Minute*1, func() {
		pconn.Close()
		conn.Close()
	})

	wg := new(sync.WaitGroup)
	wg.Add(2)
	go func() {
		for {
			/*
				+----------+----------+------+----------+----------+
				| HOST LEN | HOST     | PORT | DATA LEN | DATA     |
				+----------+----------+------+----------+----------+
				| 1        | Variable | 2    | 2        | Variable |
				+----------+----------+------+----------+----------+

				- HOST LEN: [目标|来源]地址的长度
				- HOST: [目标|来源]地址，IPv[4|6]或者域名
				- PORT: [目标|来源]端口，使用大端字节序，uint16
				- DATA LEN: 原始数据长度，使用大端字节序，uint16
				- DATA: 原始数据
			*/
			buf := make([]byte, 1)
			_, err := conn.Read(buf)
			if err != nil {
				log.Println(err)
				break
			}
			alive <- true
			hostLen := buf[0]
			buf = make([]byte, hostLen)
			_, err = conn.Read(buf)
			if err != nil {
				log.Println(err)
				break
			}
			host := string(buf)
			buf = make([]byte, 2)
			_, err = conn.Read(buf)
			if err != nil {
				log.Println(err)
				break
			}
			port := binary.BigEndian.Uint16(buf)
			buf = make([]byte, 4096)
			n, err := conn.Read(buf)
			if err != nil {
				log.Println(err)
				break
			}

			addr, err := net.ResolveUDPAddr("udp", host+":"+strconv.Itoa(int(port)))
			if err != nil {
				log.Println(err)
				break
			}
			pconn.WriteToUDP(buf[:n], addr)
		}
		pconn.Close()
		conn.Close()
		wg.Done()
		exit <- true
	}()
	go func() {
		for {
			buf := make([]byte, 4096)
			n, addr, err := pconn.ReadFromUDP(buf)
			if err != nil {
				log.Println(err)
				break
			}
			alive <- true

			/*
				+----------+----------+------+----------+----------+
				| HOST LEN | HOST     | PORT | DATA LEN | DATA     |
				+----------+----------+------+----------+----------+
				| 1        | Variable | 2    | 2        | Variable |
				+----------+----------+------+----------+----------+

				- HOST LEN: [目标|来源]地址的长度
				- HOST: [目标|来源]地址，IPv[4|6]或者域名
				- PORT: [目标|来源]端口，使用大端字节序，uint16
				- DATA LEN: 原始数据长度，使用大端字节序，uint16
				- DATA: 原始数据
			*/
			hostBytes := []byte(addr.IP.String())
			buffer := bytes.NewBuffer([]byte{byte(len(hostBytes))})
			buffer.Write(hostBytes)
			b := make([]byte, 4)
			binary.BigEndian.PutUint16(b[:2], uint16(addr.Port))
			binary.BigEndian.PutUint16(b[2:], uint16(n))
			buffer.Write(b)
			buffer.Write(buf[:n])

			_, err = pconn.Write(buffer.Bytes())
			if err != nil {
				log.Println(err)
				break
			}
		}
		pconn.Close()
		conn.Close()
		wg.Done()
		exit <- true
	}()

	wg.Wait()
	log.Println(conn.RemoteAddr(), "==udp==", "ALL", "[√]")
}

func Copy(dst io.Writer, src io.Reader) (written int64, err error) {
	buf := make([]byte, 32*1024)
	for {
		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := dst.Write(buf[0:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}
		}
		if er == io.EOF {
			break
		}
		if er != nil {
			err = er
			break
		}
	}
	return written, err
}
