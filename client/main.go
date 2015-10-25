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
	"io"
	"log"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Bluek404/Stepladder/aestcp"

	"github.com/BurntSushi/toml"
)

const VERSION = "4.0.0"

const (
	verSocks5 = 0x05

	atypIPv4Address = 0x01
	atypDomainName  = 0x03
	atypIPv6Address = 0x04

	reqtypeTCP  = 0x01
	reqtypeBIND = 0x02
	reqtypeUDP  = 0x03
)

var ipv4Reg = regexp.MustCompile(`(?:[0-9]+\.){3}[0-9]+`)

type config struct {
	Port    int `toml:"port"`
	Servers []struct {
		Host string `toml:"host"`
		Key  string `toml:"key"`
	} `toml:"server"`
}

func main() {
	var cfg config
	_, err := toml.DecodeFile("./client.toml", &cfg)
	if err != nil {
		log.Println(err)
		os.Exit(1)
	}
	if len(cfg.Servers) == 0 {
		log.Println("无法从配置文件中读取服务器列表")
		os.Exit(1)
	}

	s := serve{
		getServer: make(chan serverInfo),
		servers:   []serverInfo{},
	}

	for _, server := range cfg.Servers {
		keyB := []byte(server.Key)
		switch len(keyB) {
		case 16, 32, 64:
			break
		default:
			log.Println("KEY 长度必须为 16、32 或者 64")
			os.Exit(1)
		}
		s.servers = append(s.servers, serverInfo{server.Host, keyB})
	}
	go s.getServerLoop()

	ln, err := net.Listen("tcp", ":"+strconv.Itoa(cfg.Port))
	if err != nil {
		log.Println(err)
		os.Exit(1)
	}
	defer ln.Close()

	log.Println("|>>>>>>>>>>>>>>>|<<<<<<<<<<<<<<<|")
	log.Println("Version:", VERSION)
	log.Println("Port:", cfg.Port)
	for i, server := range cfg.Servers {
		log.Println("Server "+strconv.Itoa(i)+":", server.Host)
		log.Println("Key "+strconv.Itoa(i)+":", server.Key)
	}
	log.Println("|>>>>>>>>>>>>>>>|<<<<<<<<<<<<<<<|")

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Println(err)
			continue
		}
		go s.handleConnection(conn)
	}
}

func readHostAndPort(atype byte, conn net.Conn) (host string, port uint16, err error) {
	switch atype {
	case atypIPv4Address:
		buf := make([]byte, 4)
		_, err = conn.Read(buf)
		if err != nil {
			return "", 0, err
		}
		host = net.IP(buf).String()
	case atypIPv6Address:
		buf := make([]byte, 16)
		_, err = conn.Read(buf)
		if err != nil {
			return "", 0, err
		}
		host = net.IP(buf).String()
	case atypDomainName:
		// 读取域名长度
		buf := make([]byte, 1)
		_, err = conn.Read(buf)
		if err != nil {
			return "", 0, err
		}
		// 根据读取到的长度读取域名
		buf = make([]byte, buf[0])
		_, err = conn.Read(buf)
		if err != nil {
			return "", 0, err
		}
		host = string(buf)
	}
	// 读取端口
	err = binary.Read(io.Reader(conn), binary.BigEndian, &port)
	if err != nil {
		return "", 0, err
	}

	return host, port, nil
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

type serverInfo struct {
	host string
	key  []byte
}

type serve struct {
	getServer chan serverInfo
	servers   []serverInfo
}

func (s *serve) getServerLoop() {
	for {
		for _, server := range s.servers {
			s.getServer <- server
		}
	}
}

func (s *serve) handleConnection(conn net.Conn) {
	log.Println("[+]", conn.RemoteAddr())
	defer log.Println("[-]", conn.RemoteAddr())

	// socks5握手部分，具体参见 RFC1928
	var buf = make([]byte, 1+1+255)
	_, err := conn.Read(buf)
	if err != nil {
		log.Println(err)
		conn.Close()
		return
	}
	if buf[0] != verSocks5 {
		log.Println("使用的socks版本为", buf[0], "，需要为 5")
		conn.Write([]byte{5, 0})
		return
	}

	// 发送METHOD选择信息
	_, err = conn.Write([]byte{5, 0})
	if err != nil {
		log.Println(err)
		conn.Close()
		return
	}

	// 接收客户端需求信息
	buf = make([]byte, 4)
	_, err = conn.Read(buf)
	if err != nil {
		log.Println(err)
		conn.Close()
		return
	}

	// 判断协议
	var reqtype uint16
	switch buf[1] {
	case reqtypeTCP:
		reqtype = reqtypeTCP
	case reqtypeBIND:
		log.Println("暂不支持 BIND 命令（估计以后也不会支持）")
		conn.Write([]byte{5, 2, 0, 1, 0, 0, 0, 0, 0, 0})
		conn.Close()
		return
	case reqtypeUDP:
		reqtype = reqtypeUDP
	}

	host, port, err := readHostAndPort(buf[3], conn)
	if err != nil {
		log.Println(err)
		conn.Close()
		return
	}

	if reqtype == reqtypeTCP {
		s.proxyTCP(conn, host, port)
	} else {
		s.proxyUDP(conn, host, port)
	}
}

func (s *serve) proxyTCP(conn net.Conn, host string, port uint16) {
	// 与服务端建立链接
	server := <-s.getServer
	log.Println("[TCP]", conn.RemoteAddr(), server.host, host+":"+strconv.Itoa(int(port)), "[+]")
	pconn, err := aestcp.Dial("tcp", server.host, server.key)
	if err != nil {
		log.Println("连接服务端失败:", err)
		conn.Close()
		return
	}
	/*
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
	buffer := bytes.NewBuffer([]byte{0})
	byteHost := []byte(host)
	buffer.WriteByte(byte(len(byteHost)))
	buffer.Write(byteHost)
	buffer.Write(make([]byte, 2))
	request := buffer.Bytes()
	binary.BigEndian.PutUint16(request[len(request)-2:], port)
	_, err = pconn.Write(request)
	if err != nil {
		log.Println(err)
		pconn.Close()
		conn.Close()
		return
	}

	/*
		+------+
		| CODE |
		+------+
		| 1    |
		+------+

		- CODE: 状态码。0为成功，[1|3-5]为socks5相应状态码
	*/
	// 读取服务端返回状态
	buf := make([]byte, 1)
	_, err = pconn.Read(buf)
	if err != nil {
		log.Println(err)
		pconn.Close()
		conn.Close()
		return
	}

	/*
		SOCKS5状态码:
		1: General SOCKS server failure
		3: Network unreachable
		4: Host unreachable
		5: Connection refused
	*/
	code := buf[0]

	// 回应消息
	_, err = conn.Write([]byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0})
	if err != nil {
		log.Println(err)
		pconn.Close()
		conn.Close()
		return
	}

	// 检查状态码
	// 放在这里是因为要先回应消息
	if code != 0 {
		log.Println("[TCP]", conn.RemoteAddr(), server.host, host+":"+strconv.Itoa(int(port)), "[×]")
		pconn.Close()
		conn.Close()
		return
	}

	wg := new(sync.WaitGroup)
	wg.Add(2)
	go func() {
		Copy(pconn, conn)
		pconn.Close()
		conn.Close()
		wg.Done()
	}()
	go func() {
		Copy(conn, pconn)
		pconn.Close()
		conn.Close()
		wg.Done()
	}()

	wg.Wait()
	log.Println("[TCP]", conn.RemoteAddr(), server.host, host+":"+strconv.Itoa(int(port)), "[√]")
}

func (s *serve) proxyUDP(conn net.Conn, host string, port uint16) {
	// 与服务端建立链接
	server := <-s.getServer
	log.Println("[UDP]", conn.RemoteAddr(), server.host, "*", "[+]")
	pconn, err := aestcp.Dial("tcp", server.host, server.key)
	if err != nil {
		log.Println("连接服务端失败:", err)
		conn.Close()
		return
	}
	/*
		+-----+
		| CMD |
		+-----+
		| 1   |
		+-----+

		- CMD: 协议类型。0为TCP，1为UDP
	*/
	_, err = pconn.Write([]byte{1})
	if err != nil {
		log.Println(err)
		pconn.Close()
		conn.Close()
		return
	}

	/*
		+------+
		| CODE |
		+------+
		| 1    |
		+------+

		- CODE: 状态码。0为成功，2为session无效，[1|3-5]为socks5相应状态码
	*/
	// 读取服务端返回状态
	buf := make([]byte, 1)
	_, err = pconn.Read(buf)
	if err != nil {
		log.Println(err)
		pconn.Close()
		conn.Close()
		return
	}

	/*
		SOCKS5状态码:
		1: General SOCKS server failure
		3: Network unreachable
		4: Host unreachable
		5: Connection refused
	*/
	code := buf[0]

	// 检查状态码
	if code != 0 {
		log.Println("[UDP]", conn.RemoteAddr(), server.host, "*", "[×]")
		conn.Write([]byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0})
		conn.Close()
		pconn.Close()
		return
	}

	uconn, err := net.ListenUDP("udp", nil)
	if err != nil {
		log.Println(err)
		conn.Write([]byte{1})
		conn.Close()
		pconn.Close()
		return
	}
	buffer := bytes.NewBuffer([]byte{5, code, 0})
	lAddr := conn.LocalAddr().String()
	lAddr = lAddr[:strings.LastIndex(lAddr, ":")]
	if ipv4Reg.MatchString(lAddr) {
		buffer.WriteByte(atypIPv4Address)
		ipv6 := net.ParseIP(lAddr)
		ipv4 := ipv6[len(ipv6)-4:]
		buffer.Write(ipv4)
	} else if strings.ContainsRune(lAddr, ':') {
		buffer.WriteByte(atypIPv6Address)
		buffer.Write(net.ParseIP(lAddr))
	} else {
		buffer.WriteByte(atypDomainName)
		byteAddr := []byte(lAddr)
		buffer.WriteByte(byte(len(byteAddr)))
		buffer.Write(byteAddr)
	}
	buffer.Write(make([]byte, 2))
	response := buffer.Bytes()
	strPort := uconn.LocalAddr().String()
	strPort = strPort[strings.LastIndex(strPort, ":")+1:]
	lPort, err := strconv.Atoi(strPort)
	if err != nil {
		log.Println(err)
		conn.Close()
		pconn.Close()
		return
	}
	binary.BigEndian.PutUint16(response[len(response)-2:],
		uint16(lPort))
	// 回应消息
	_, err = conn.Write(response)
	if err != nil {
		log.Println(err)
		conn.Close()
		pconn.Close()
		uconn.Close()
		return
	}
	conn.Close()

	rAddr, err := net.ResolveUDPAddr("udp", host+":"+strconv.Itoa(int(port)))
	if err != nil {
		log.Println(err)
		pconn.Close()
		uconn.Close()
		return
	}

	alive, exit := newTimeouter(time.Minute*1, func() {
		pconn.Close()
		uconn.Close()
	})

	wg := new(sync.WaitGroup)
	wg.Add(2)
	go func() {
		for {
			buf := make([]byte, 4)
			_, addr, err := uconn.ReadFromUDP(buf)
			if err != nil {
				log.Println(err)
				break
			}
			if buf[3] != 0 || addr != rAddr {
				continue
			}
			alive <- true

			pHost, pPort, err := readHostAndPort(buf[3], conn)
			if err != nil {
				log.Println(err)
				break
			}
			buf = make([]byte, 4096)
			n, _, err := uconn.ReadFromUDP(buf)
			if err != nil {
				log.Println(err)
				break
			}

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
			pHostBytes := []byte(pHost)
			buffer := bytes.NewBuffer([]byte{byte(len(pHostBytes))})
			buffer.Write(pHostBytes)
			b := make([]byte, 4)
			binary.BigEndian.PutUint16(b[:2], pPort)
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
		uconn.Close()
		wg.Done()
		exit <- true
	}()
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
			_, err := pconn.Read(buf)
			if err != nil {
				log.Println(err)
				break
			}
			alive <- true
			hostLen := buf[0]
			buf = make([]byte, hostLen)
			_, err = pconn.Read(buf)
			if err != nil {
				log.Println(err)
				break
			}
			host := string(buf)
			buf = make([]byte, 2)
			_, err = pconn.Read(buf)
			if err != nil {
				log.Println(err)
				break
			}
			port := buf
			buf = make([]byte, 4096)
			n, err := pconn.Read(buf)
			if err != nil {
				log.Println(err)
				break
			}

			buffer := bytes.NewBuffer([]byte{0, 0, 0})
			if ipv4Reg.MatchString(host) {
				buffer.WriteByte(atypIPv4Address)
				ipv6 := net.ParseIP(host)
				ipv4 := ipv6[len(ipv6)-4:]
				buffer.Write(ipv4)
			} else if strings.ContainsRune(host, ':') {
				buffer.WriteByte(atypIPv6Address)
				buffer.Write(net.ParseIP(host))
			} else {
				buffer.WriteByte(atypDomainName)
				byteAddr := []byte(host)
				buffer.WriteByte(byte(len(byteAddr)))
				buffer.Write(byteAddr)
			}
			buffer.Write(port)
			b := make([]byte, 2)
			binary.BigEndian.PutUint16(b, uint16(n))
			buffer.Write(b)
			buffer.Write(buf[:n])
			_, err = uconn.WriteToUDP(buffer.Bytes(), rAddr)
			if err != nil {
				log.Println(err)
				break
			}
		}
		pconn.Close()
		uconn.Close()
		wg.Done()
		exit <- true
	}()

	wg.Wait()
	log.Println("[UDP]", conn.RemoteAddr(), server.host, "*", "[√]")
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
