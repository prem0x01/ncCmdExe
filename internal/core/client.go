package core

import (
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var (
	clientStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#00BFFF")).
		Bold(true)
)

type Client struct {
	host    string
	port    int
	udp     bool
	timeout int
}

func NewClient(host string, port int, udp bool, timeout int) *Client {
	return &Client{
		host:    host,
		port:    port,
		udp:     udp,
		timeout: timeout,
	}

}

func (c *Client) TestConnection() error {
	addr := fmt.Sprintf("%s:%d", c.host, c.port)
	conn, err := net.DialTimeout("tcp", addr, time.Duration(c.timeout)*time.Second)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

func (c *Client) Connect() {
	protocol := "tcp"
	if c.udp {
		protocol = "udp"
	}

	addr := fmt.Sprintf("%s:%d", c.host, c.port)

	conn, err := net.DialTimeout(protocol, addr, time.Duration(c.timeout)*time.Second)
	if err != nil {
		fmt.Printf("Failed to connect to %s: %v\n", addr, err)
		return
	}
	defer conn.Close()

	fmt.Println(clientStyle.Render(fmt.Sprintf("Connected to %s://%s", protocol, addr)))

	go io.Copy(conn, os.Stdin)
	io.Copy(os.Stdout, conn)
}
