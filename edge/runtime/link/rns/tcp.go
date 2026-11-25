package rns

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"
)

// hwMTU bounds a partially received frame, as the TCP interface's hardware MTU does.
const hwMTU = 262144

// TCPClient is a Reticulum TCP client interface: it connects to a TCP server interface (rnsd or any
// Reticulum instance), exchanges HDLC-framed packets and reconnects after the connection drops.
type TCPClient struct {
	addr      string
	dialer    net.Dialer
	reconnect time.Duration

	mu       sync.Mutex
	conn     net.Conn
	incoming chan []byte
	online   chan bool
}

// NewTCPClient prepares a client for host:port; Run connects it.
func NewTCPClient(addr string) *TCPClient {
	return &TCPClient{
		addr:      addr,
		dialer:    net.Dialer{Timeout: 5 * time.Second, KeepAlive: 5 * time.Second},
		reconnect: 5 * time.Second,
		incoming:  make(chan []byte, 256),
		online:    make(chan bool, 4),
	}
}

// Incoming delivers received packets.
func (c *TCPClient) Incoming() <-chan []byte { return c.incoming }

// Online reports connects (true) and disconnects (false).
func (c *TCPClient) Online() <-chan bool { return c.online }

// Run keeps the connection up until ctx ends.
func (c *TCPClient) Run(ctx context.Context) {
	defer close(c.incoming)
	for ctx.Err() == nil {
		conn, err := c.dialer.DialContext(ctx, "tcp", c.addr)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(c.reconnect):
				continue
			}
		}
		c.setConn(conn)
		c.notify(true)
		c.read(ctx, conn)
		c.setConn(nil)
		c.notify(false)
	}
}

func (c *TCPClient) read(ctx context.Context, conn net.Conn) {
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	defer func() { _ = conn.Close() }()
	d := deframer{maxFrame: hwMTU}
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		for _, p := range d.write(buf[:n]) {
			select {
			case c.incoming <- p:
			default:
			}
		}
		if err != nil {
			return
		}
	}
}

// Send writes one packet; it fails while disconnected.
func (c *TCPClient) Send(packet []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return errors.New("rns: TCP interface is offline")
	}
	_, err := c.conn.Write(hdlcFrame(packet))
	return err
}

func (c *TCPClient) setConn(conn net.Conn) {
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
}

func (c *TCPClient) notify(up bool) {
	select {
	case c.online <- up:
	default:
	}
}
