package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/FoxDenHome/sni-vhost-proxy/util"
	"github.com/inconshreveable/go-vhost"
)

// Last bit here indicates version 2, PROXIED
var ProxyProtocolHeader = [13]byte{0x0D, 0x0A, 0x0D, 0x0A, 0x00, 0x0D, 0x0A, 0x51, 0x55, 0x49, 0x54, 0x0A, 0b00100001}

// These always indicate STREAM (TCP)
const ProxyAFIPv4 = 0b00010001
const ProxyAFIPv6 = 0b00100001

// src address + src port + dst address + dst port
const AddrLenIPv4 = (net.IPv4len + 2) * 2
const AddrLenIPv6 = (net.IPv6len + 2) * 2

var initWait sync.WaitGroup
var listenerClosedWait sync.WaitGroup
var privilegeDropWait sync.WaitGroup

func makeProxyProtocolPayload(conn net.Conn) ([]byte, error) {
	srcAddr := conn.RemoteAddr().(*net.TCPAddr)
	dstAddr := conn.LocalAddr().(*net.TCPAddr)

	maxAddrLen := len(srcAddr.IP)
	if len(dstAddr.IP) > maxAddrLen {
		maxAddrLen = len(dstAddr.IP)
	}

	outBuf := bytes.Buffer{}
	outBuf.Write(ProxyProtocolHeader[:])

	switch maxAddrLen {
	case net.IPv4len:
		outBuf.WriteByte(ProxyAFIPv4)
		binary.Write(&outBuf, binary.BigEndian, uint16(AddrLenIPv4))
		outBuf.Write(srcAddr.IP.To4())
		outBuf.Write(dstAddr.IP.To4())
	case net.IPv6len:
		outBuf.WriteByte(ProxyAFIPv6)
		binary.Write(&outBuf, binary.BigEndian, uint16(AddrLenIPv6))
		outBuf.Write(srcAddr.IP.To16())
		outBuf.Write(dstAddr.IP.To16())
	default:
		return nil, fmt.Errorf("unknown address family len %d", maxAddrLen)
	}

	binary.Write(&outBuf, binary.BigEndian, uint16(srcAddr.Port))
	binary.Write(&outBuf, binary.BigEndian, uint16(dstAddr.Port))

	return outBuf.Bytes(), nil
}

func handleConnection(client net.Conn, protocol BackendProtocol) {
	defer client.Close()

	var vhostConn vhost.Conn
	var err error
	switch protocol {
	case PROTO_HTTP:
		vhostConn, err = vhost.HTTP(client)
	case PROTO_HTTPS:
		vhostConn, err = vhost.TLS(client)
	default:
		log.Printf("Invalid protocol from %v", client.RemoteAddr())
		return
	}
	if err != nil {
		if verbose {
			log.Printf("Error decoding protocol from %v: %v", client.RemoteAddr(), err)
		}
		return
	}

	hostname := strings.ToLower(vhostConn.Host())
	vhostConn.Free()
	backend, err := GetBackend(hostname, protocol)
	if err != nil {
		log.Printf("Couldn't get backend for %s: %v", hostname, err)
		return
	}

	if backend == nil {
		// This means we don't want to handle the connection
		return
	}

	useHost := backend.Host
	if backend.HostPassthrough {
		useHost = hostname
	}

	ipport := fmt.Sprintf("%s:%d", useHost, backend.Port)

	upConn, err := net.DialTimeout("tcp", ipport, time.Duration(10000)*time.Millisecond)
	if err != nil {
		log.Printf("Couldn't dial backend connection for %s: %v", hostname, err)
		return
	}
	defer upConn.Close()

	if backend.ProxyProtocol {
		data, err := makeProxyProtocolPayload(client)
		if err != nil {
			log.Printf("Could not make PROXY protocol payload for %s: %v", hostname, err)
			return
		}
		_, err = upConn.Write(data)
		if err != nil {
			log.Printf("Could not write PROXY protocol payload for %s: %v", hostname, err)
			return
		}
	}

	joinConnections(vhostConn, upConn)
}

func halfJoin(wg *sync.WaitGroup, dst net.Conn, src net.Conn) {
	defer wg.Done()
	defer dst.Close()
	defer src.Close()
	_, err := io.Copy(dst, src)
	if err == nil || errors.Is(err, net.ErrClosed) {
		return
	}
	if verbose {
		log.Printf("Proxy copy from %v to %v failed with error %v", src.RemoteAddr(), dst.RemoteAddr(), err)
	}
}

func joinConnections(c1 net.Conn, c2 net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go halfJoin(&wg, c1, c2)
	go halfJoin(&wg, c2, c1)
	wg.Wait()
}

func doProxy(host string, protocol BackendProtocol) {
	defer func() {
		listenerClosedWait.Done()
		log.Panicf("listener goroutine ended unexpectedly")
	}()

	listener, err := net.Listen("tcp", host)
	initWait.Done()
	if err != nil {
		log.Panicf("could not listen: %v", err)
		return
	}

	log.Printf("Listener started on %s", host)
	privilegeDropWait.Wait()

	log.Printf("Server started on %s", host)
	for {
		connection, err := listener.Accept()
		if err != nil {
			log.Printf("Accept error: %v", err)
			return
		}

		go handleConnection(connection, protocol)
	}
}

func main() {
	LoadConfig()

	privilegeDropWait.Add(1)

	initWait.Add(2)
	listenerClosedWait.Add(2)
	go doProxy(os.Getenv("HTTP_ADDR"), PROTO_HTTP)
	go doProxy(os.Getenv("HTTPS_ADDR"), PROTO_HTTPS)

	initWait.Wait()
	util.DropPrivs()
	privilegeDropWait.Done()

	listenerClosedWait.Wait()
}
