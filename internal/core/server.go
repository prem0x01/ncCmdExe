package core

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	serverStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#00FF00")).
		Bold(true)
)

type Server struct {
	port    int
	udp     bool
	execute string
	shell   bool
}

func NewServer(port int, udp bool, execute string, shell bool) *Server {
	return &Server{
		port:    port,
		udp:     udp,
		execute: execute,
		shell:   shell,
	}
}

func (s *Server) Start() {
	protocol := "tcp"
	if s.udp {
		protocol = "udp"
	}

	addr := fmt.Sprintf(":%d", s.port)
	listener, err := net.Listen(protocol, addr)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	defer listener.Close()

	fmt.Println(serverStyle.Render(fmt.Sprintf("Server listening on %s://%s", protocol, addr)))

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Panicf("Failed to accept connections: %v", err)
			continue
		}

		go s.handleConnection(conn)
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	clientAddr := conn.RemoteAddr().String()
	fmt.Println(serverStyle.Render(fmt.Sprintf("New connection from %s", clientAddr)))

	if s.execute != "" {
		s.executeCommand(conn, s.execute)
	} else if s.shell {
		s.spawnShell(conn)
	} else {
		s.relay(conn)
	}
}

func (s *Server) executeCommand(conn net.Conn, command string) {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return
	}

	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Stdin = conn
	cmd.Stdout = NewFlusher(conn)
	cmd.Stderr = NewFlusher(conn)

	if err := cmd.Run(); err != nil {
		fmt.Fprintf(conn, "Error executing command: %v\n", err)
	}
}

func (s *Server) spawnShell(conn net.Conn) {
	cmd := exec.Command("/bin/bash", "-i")
	cmd.Stdin = conn
	cmd.Stdout = NewFlusher(conn)
	cmd.Stderr = NewFlusher(conn)

	if err := cmd.Run(); err != nil {
		fmt.Fprintf(conn, "Error spawing shell: %v\n", err)
	}
}

func (s *Server) relay(conn net.Conn) {
	go io.Copy(conn, os.Stdin)
	io.Copy(os.Stdout, conn)
}

type Flusher struct {
	w *bufio.Writer
}

func NewFlusher(w io.Writer) *Flusher {
	return &Flusher{
		w: bufio.NewWriter(w),
	}
}

func (f *Flusher) Write(b []byte) (int, error) {
	count, err := f.w.Write(b)
	if err != nil {
		return count, err
	}
	if err := f.w.Flush(); err != nil {
		return count, err
	}
	return count, nil
}
