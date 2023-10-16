package main

import (
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
)

func main() {
	err := main2()
	if err != nil {
		panic(fmt.Sprintf("err: %s", err))
	}
}

func main2() error {
	if len(os.Args) != 6 {
		return fmt.Errorf("Expected 5 args: usage: proxy <proxy-port> <server-addr> <server-port> <server-cert-path> <server-key-path>")
	}

	port := os.Args[1]
	targetAddr := os.Args[2]
	targetPort := os.Args[3]
	serverCertPath := os.Args[4]
	serverKeyPath := os.Args[5]

	serverCert, err := tls.LoadX509KeyPair(serverCertPath, serverKeyPath)
	if err != nil {
		return fmt.Errorf("loading server key pair: %s", err)
	}

	tlsConfig := tls.Config{Certificates: []tls.Certificate{serverCert}}
	tlsConfig.Rand = rand.Reader

	proxy := MysqlProxy{
		listenAddr: fmt.Sprintf("0.0.0.0:%s", port),
		serverAddr: fmt.Sprintf("%s:%s", targetAddr, targetPort),
		tlsConfig:  tlsConfig,
		debug:      false,
	}

	return proxy.Serve()
}

type MysqlProxy struct {
	listenAddr string
	serverAddr string
	tlsConfig  tls.Config
	debug      bool
}

func (p MysqlProxy) Serve() error {
	lis, err := net.Listen("tcp", p.listenAddr)
	if err != nil {
		return fmt.Errorf("listening: %s", err)
	}

	for {
		clientConn, err := lis.Accept()
		if err != nil {
			fmt.Printf("listener accept err: %s\n", err)
			continue
		}

		go p.serveConn(clientConn)
	}
}

func (p MysqlProxy) serveConn(rawClientConn net.Conn) {
	defer rawClientConn.Close()

	rawServerConn, err := net.Dial("tcp", p.serverAddr)
	if err != nil {
		fmt.Printf("failed dialing server: %s\n", err)
		return
	}

	defer rawServerConn.Close()

	serverConn, clientConn, err := p.connectServerAndClient(rawClientConn, rawServerConn)
	if err != nil {
		fmt.Printf("failed establish server+client conn: %s", err)
		return
	}

	fmt.Printf("connected server and client\n")

	var wg sync.WaitGroup

	wg.Add(2)

	go ConnCopier{}.DstToSrcCopy(serverConn.Conn, clientConn.Conn, &wg)
	go ConnCopier{}.SrcToDstCopy(serverConn.Conn, clientConn.Conn, &wg)

	wg.Wait()

	err = serverConn.Conn.Close()
	if err != nil {
		if err != io.EOF {
			fmt.Printf("failed close server conn: %s\n", err)
		}
	}

	err = clientConn.Conn.Close()
	if err != nil {
		if err != io.EOF {
			fmt.Printf("failed close client conn: %s\n", err)
		}
	}

	fmt.Printf("disconnected server and client\n")
}

func (p MysqlProxy) connectServerAndClient(rawClientConn net.Conn, rawServerConn net.Conn) (*ReadableConn, *ReadableConn, error) {
	clientConn := NewReadableConn(rawClientConn, "client")
	serverConn := NewReadableConn(rawServerConn, "server")

	serverHandshakeBytes, err := p.readPacket(serverConn)
	if err != nil {
		return serverConn, clientConn, fmt.Errorf("failed reading handshake from server: %s\n", err)
	}

	err = p.writePacket(clientConn, serverHandshakeBytes)
	if err != nil {
		return serverConn, clientConn, fmt.Errorf("failed forwarding handshake to client: %s\n", err)
	}

	clientHandshakeBytes, err := p.readPacket(clientConn)
	if err != nil {
		return serverConn, clientConn, fmt.Errorf("failed reading handshake from client: %s\n", err)
	}

	// https://dev.mysql.com/doc/internals/en/connection-phase-packets.html#packet-Protocol::SSLRequest
	const tlsInitPktLen = 32
	wantsTLS := clientHandshakeBytes[0] == tlsInitPktLen

	if wantsTLS {
		clientConn = NewReadableConn(tls.Server(clientConn.Conn, &p.tlsConfig), "client-tls")

		// do not send initial tls pkt to server
		var err error

		clientHandshakeBytes, err = p.readPacket(clientConn)
		if err != nil {
			return serverConn, clientConn, fmt.Errorf("failed reading handshake 2 from client: %s\n", err)
		}

		// adjust seq to 1 from 2 (server did not see init tls packet)
		clientHandshakeBytes[3] = 0x1
		// disable clientSSL flag in handshake
		clientHandshakeBytes[5] = byte(p.clearBit(int(clientHandshakeBytes[5]), 3))
	}

	err = p.writePacket(serverConn, clientHandshakeBytes)
	if err != nil {
		return serverConn, clientConn, fmt.Errorf("failed forwarding handshake to server: %s\n", err)
	}

	authRespBytes, err := p.readPacket(serverConn)
	if err != nil {
		return serverConn, clientConn, fmt.Errorf("failed reading auth resp from server: %s\n", err)
	}

	if wantsTLS {
		authRespBytes[3] = 0x3
	}

	err = p.writePacket(clientConn, authRespBytes)
	if err != nil {
		return serverConn, clientConn, fmt.Errorf("failed forwarding auth resp to client: %s\n", err)
	}

	return serverConn, clientConn, nil
}

func (p MysqlProxy) readPacket(conn *ReadableConn) ([]byte, error) {
	headerBytes, err := conn.ReadN(4)
	if err != nil {
		return nil, fmt.Errorf("reading header: %s", err)
	}

	if headerBytes[0] > 0 && headerBytes[1] == 0 && headerBytes[2] == 0 {
		dataBytes, err := conn.ReadN(int(headerBytes[0]))
		if err != nil {
			return nil, fmt.Errorf("reading data: %s", err)
		}

		bytes := append(headerBytes, dataBytes...)

		if p.debug {
			fmt.Printf("read len=%d from %s: %#v\n\n", len(headerBytes)+len(dataBytes), conn.Tag, bytes)
		}

		return bytes, nil
	}

	return nil, fmt.Errorf("unexpected length")
}

func (p MysqlProxy) writePacket(conn *ReadableConn, bytes []byte) error {
	if p.debug {
		fmt.Printf("write len=%d to %s: %#v\n\n", len(bytes), conn.Tag, bytes)
	}

	_, err := conn.Conn.Write(bytes)
	if err != nil {
		return fmt.Errorf("writing data: %s\n", err)
	}

	return nil
}

func (MysqlProxy) clearBit(n int, pos uint) int {
	mask := ^(1 << pos)
	n &= mask
	return n
}

type ReadableConn struct {
	Conn net.Conn
	Tag  string
}

func NewReadableConn(conn net.Conn, tag string) *ReadableConn {
	return &ReadableConn{conn, tag}
}

func (c *ReadableConn) ReadN(n int) ([]byte, error) {
	readBytes := make([]byte, n)
	tmpBytes := make([]byte, n) // todo reset?

	for i := 0; i < n; {
		readN, err := c.Conn.Read(tmpBytes)
		if err != nil {
			return nil, fmt.Errorf("Reading %v+%v: %s", readBytes, tmpBytes, err)
		}
		copy(readBytes[i:], tmpBytes[:c.minInt(n-i, readN)])
		i += readN
	}

	if n != len(readBytes) {
		panic(fmt.Sprintf("Expected to read '%d' bytes but got '%d'", n, len(readBytes)))
	}

	return readBytes, nil
}

func (ReadableConn) minInt(a, b int) int {
	if a > b {
		return b
	}
	return a
}

type ConnCopier struct{}

func (c ConnCopier) SrcToDstCopy(dstConn net.Conn, srcConn net.Conn, wg *sync.WaitGroup) {
	_, err := io.Copy(dstConn, srcConn)
	if err != nil {
		fmt.Printf("conn copier: Failed to copy src->dst conn: %s\n", err)
	}

	fmt.Printf("copy finished src->dst\n")

	wg.Done()
}

func (c ConnCopier) DstToSrcCopy(dstConn net.Conn, srcConn net.Conn, wg *sync.WaitGroup) {
	_, err := io.Copy(srcConn, dstConn)
	if err != nil {
		fmt.Printf("conn copier: Failed to copy dst->src conn: %s\n", err)
	}

	fmt.Printf("copy finished dst->src\n")

	wg.Done()
}
